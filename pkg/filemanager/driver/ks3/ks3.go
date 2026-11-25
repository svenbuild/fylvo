package ks3

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go/aws/request"

	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"strconv"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/boolset"
	"github.com/cloudreve/Cloudreve/v4/pkg/cluster/routes"
	"github.com/cloudreve/Cloudreve/v4/pkg/conf"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/chunk"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/chunk/backoff"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/driver"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs/mime"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/cloudreve/Cloudreve/v4/pkg/setting"
	"github.com/ks3sdklib/aws-sdk-go/aws/awserr"
	"github.com/ks3sdklib/aws-sdk-go/service/s3/s3manager"
	"github.com/samber/lo"

	"github.com/ks3sdklib/aws-sdk-go/aws"
	"github.com/ks3sdklib/aws-sdk-go/aws/credentials"

	"github.com/ks3sdklib/aws-sdk-go/service/s3"
)

// Driver KS3 compatible driver
type Driver struct {
	policy    *ent.StoragePolicy
	chunkSize int64

	settings setting.Provider
	l        logging.Logger
	config   conf.ConfigProvider
	mime     mime.MimeDetector

	sess *aws.Config
	svc  *s3.S3
}

// UploadPolicy KS3 upload policy
type UploadPolicy struct {
	Expiration string        `json:"expiration"`
	Conditions []interface{} `json:"conditions"`
}

type Session struct {
	Config   *aws.Config
	Handlers request.Handlers
}

// MetaData file info
type MetaData struct {
	Size int64
	Etag string
}

var (
	features = &boolset.BooleanSet{}
)

func init() {
	boolset.Sets(map[driver.HandlerCapability]bool{
		driver.HandlerCapabilityUploadSentinelRequired: true,
	}, features)
}

func Int64(v int64) *int64 {
	return &v
}

func New(ctx context.Context, policy *ent.StoragePolicy, settings setting.Provider,
	config conf.ConfigProvider, l logging.Logger, mime mime.MimeDetector) (*Driver, error) {
	chunkSize := policy.Settings.ChunkSize
	if policy.Settings.ChunkSize == 0 {
		chunkSize = 25 << 20 // 25 MB
	}

	driver := &Driver{
		policy:    policy,
		settings:  settings,
		chunkSize: chunkSize,
		config:    config,
		l:         l,
		mime:      mime,
	}

	sess := aws.Config{
		Credentials:      credentials.NewStaticCredentials(policy.AccessKey, policy.SecretKey, ""),
		Endpoint:         policy.Server,
		Region:           policy.Settings.Region,
		S3ForcePathStyle: policy.Settings.S3ForcePathStyle,
	}
	driver.sess = &sess
	driver.svc = s3.New(&sess)

	return driver, nil
}

// List lists files under the given path
func (handler *Driver) List(ctx context.Context, base string, onProgress driver.ListProgressFunc, recursive bool) ([]fs.PhysicalObject, error) {
	// initialize the list parameters
	base = strings.TrimPrefix(base, "/")
	if base != "" {
		base += "/"
	}

	opt := &s3.ListObjectsInput{
		Bucket:  &handler.policy.BucketName,
		Prefix:  &base,
		MaxKeys: Int64(1000),
	}

	// whether to list recursively
	if !recursive {
		opt.Delimiter = aws.String("/")
	}

	var (
		objects []*s3.Object
		commons []*s3.CommonPrefix
	)

	for {
		res, err := handler.svc.ListObjectsWithContext(ctx, opt)
		if err != nil {
			return nil, err
		}
		objects = append(objects, res.Contents...)
		commons = append(commons, res.CommonPrefixes...)

		// if the listing is not complete, continue with the marker
		if *res.IsTruncated {
			opt.Marker = res.NextMarker
		} else {
			break
		}
	}

	// process the listing result
	res := make([]fs.PhysicalObject, 0, len(objects)+len(commons))

	// process directories
	for _, object := range commons {
		rel, err := filepath.Rel(*opt.Prefix, *object.Prefix)
		if err != nil {
			continue
		}
		res = append(res, fs.PhysicalObject{
			Name:         path.Base(*object.Prefix),
			RelativePath: filepath.ToSlash(rel),
			Size:         0,
			IsDir:        true,
			LastModify:   time.Now(),
		})
	}
	onProgress(len(commons))

	// process files
	for _, object := range objects {
		if strings.HasSuffix(*object.Key, "/") && *object.Size == 0 {
			continue
		}
		rel, err := filepath.Rel(*opt.Prefix, *object.Key)
		if err != nil {
			continue
		}
		res = append(res, fs.PhysicalObject{
			Name:         path.Base(*object.Key),
			Source:       *object.Key,
			RelativePath: filepath.ToSlash(rel),
			Size:         *object.Size,
			IsDir:        false,
			LastModify:   time.Now(),
		})
	}
	onProgress(len(objects))

	return res, nil

}

// Open opens a file
func (handler *Driver) Open(ctx context.Context, path string) (*os.File, error) {
	return nil, errors.New("not implemented")
}

// Put saves the file stream to the given path
func (handler *Driver) Put(ctx context.Context, file *fs.UploadRequest) error {
	defer file.Close()

	// whether overwriting is allowed
	overwrite := file.Mode&fs.ModeOverwrite == fs.ModeOverwrite
	if !overwrite {
		// Check for duplicated file
		if _, err := handler.Meta(ctx, file.Props.SavePath); err == nil {
			return fs.ErrFileExisted
		}
	}

	// initialize the config
	uploader := s3manager.NewUploader(&s3manager.UploadOptions{
		S3:       handler.svc,       // S3Client instance, required
		PartSize: handler.chunkSize, // part size, defaults to 5MB, optional
	})

	mimeType := file.Props.MimeType
	if mimeType == "" {
		mimeType = handler.mime.TypeByName(file.Props.Uri.Name())
	}

	_, err := uploader.UploadWithContext(ctx, &s3manager.UploadInput{
		Bucket:      &handler.policy.BucketName,
		Key:         &file.Props.SavePath,
		Body:        io.LimitReader(file, file.Props.Size),
		ContentType: aws.String(mimeType),
	})

	if err != nil {
		return err
	}

	return nil
}

// Delete deletes files
func (handler *Driver) Delete(ctx context.Context, files ...string) ([]string, error) {
	failed := make([]string, 0, len(files))
	batchSize := handler.policy.Settings.S3DeleteBatchSize
	if batchSize == 0 {
		// https://docs.aws.amazon.com/AmazonS3/latest/API/API_DeleteObjects.html
		// The request can contain a list of up to 1000 keys that you want to delete.
		batchSize = 1000
	}

	var lastErr error

	groups := lo.Chunk(files, batchSize)
	for _, group := range groups {
		if len(group) == 1 {
			// Invoke single file delete API
			_, err := handler.svc.DeleteObjectWithContext(ctx, &s3.DeleteObjectInput{
				Bucket: &handler.policy.BucketName,
				Key:    &group[0],
			})

			if err != nil {
				if aerr, ok := err.(awserr.Error); ok {
					// Ignore NoSuchKey error
					if aerr.Code() == s3.ErrCodeNoSuchKey {
						continue
					}
				}
				failed = append(failed, group[0])
				lastErr = err
			}
		} else {
			// Invoke batch delete API
			res, err := handler.svc.DeleteObjects(
				&s3.DeleteObjectsInput{
					Bucket: &handler.policy.BucketName,
					Delete: &s3.Delete{
						Objects: lo.Map(group, func(s string, i int) *s3.ObjectIdentifier {
							return &s3.ObjectIdentifier{Key: &s}
						}),
					},
				})

			if err != nil {
				failed = append(failed, group...)
				lastErr = err
				continue
			}

			for _, v := range res.Errors {
				handler.l.Debug("Failed to delete file: %s, Code:%s, Message:%s", v.Key, v.Code, v.Key)
				failed = append(failed, *v.Key)
			}
		}
	}

	return failed, lastErr

}

// Thumb gets the thumbnail URL
func (handler *Driver) Thumb(ctx context.Context, expire *time.Time, ext string, e fs.Entity) (string, error) {
	w, h := handler.settings.ThumbSize(ctx)
	thumbParam := fmt.Sprintf("@base@tag=imgScale&m=0&w=%d&h=%d", w, h)

	enco := handler.settings.ThumbEncode(ctx)
	switch enco.Format {
	case "jpg", "webp":
		thumbParam += fmt.Sprintf("&q=%d&F=%s", enco.Quality, enco.Format)
	case "png":
		thumbParam += fmt.Sprintf("&F=%s", enco.Format)
	}

	// make sure the expiry is not below 0, otherwise set it to 7 days
	var ttl int64
	if expire != nil {
		ttl = int64(time.Until(*expire).Seconds())
	} else {
		ttl = 604800
	}

	thumbUrl, err := handler.svc.GeneratePresignedUrl(&s3.GeneratePresignedUrlInput{
		HTTPMethod: s3.GET,                              // request method
		Bucket:     &handler.policy.BucketName,          // bucket name
		Key:        aws.String(e.Source() + thumbParam), // object key
		Expires:    ttl,                                 // expiry, converted to seconds
	})

	if err != nil {
		return "", err
	}

	// replace the domain of the signed URL with the user's custom accelerated domain (if any)
	finalThumbURL, err := url.Parse(thumbUrl)
	if err != nil {
		return "", err
	}

	// for public buckets, strip the key and unsupported headers
	if !handler.policy.IsPrivate {
		finalThumbURL.RawQuery = ""
	}

	return finalThumbURL.String(), nil
}

// Source gets the file's external link
func (handler *Driver) Source(ctx context.Context, e fs.Entity, args *driver.GetSourceArgs) (string, error) {
	var contentDescription *string
	if args.IsDownload {
		encodedFilename := url.PathEscape(args.DisplayName)
		contentDescription = aws.String(fmt.Sprintf(`attachment; filename=%s`, encodedFilename))
	}

	// make sure the expiry is not below 0, otherwise set it to 7 days
	var ttl int64
	if args.Expire != nil {
		ttl = int64(time.Until(*args.Expire).Seconds())
	} else {
		ttl = 604800
	}

	downloadUrl, err := handler.svc.GeneratePresignedUrl(&s3.GeneratePresignedUrlInput{
		HTTPMethod:                 s3.GET,                     // request method
		Bucket:                     &handler.policy.BucketName, // bucket name
		Key:                        aws.String(e.Source()),     // object key
		Expires:                    ttl,                        // expiry, converted to seconds
		ResponseContentDisposition: contentDescription,         // set the Content-Disposition response header
	})

	if err != nil {
		return "", err
	}

	// replace the domain of the signed URL with the user's custom accelerated domain (if any)
	finalURL, err := url.Parse(downloadUrl)
	if err != nil {
		return "", err
	}

	// for public buckets, strip the key and unsupported headers
	if !handler.policy.IsPrivate {
		finalURL.RawQuery = ""
	}

	return finalURL.String(), nil
}

// Token gets the upload credential
func (handler *Driver) Token(ctx context.Context, uploadSession *fs.UploadSession, file *fs.UploadRequest) (*fs.UploadCredential, error) {
	// Check for duplicated file
	if _, err := handler.Meta(ctx, file.Props.SavePath); err == nil {
		return nil, fs.ErrFileExisted
	}

	// generate the callback URL
	siteURL := handler.settings.SiteURL(setting.UseFirstSiteUrl(ctx))
	// create the upload session on the slave node
	uploadSession.ChunkSize = handler.chunkSize
	uploadSession.Callback = routes.MasterSlaveCallbackUrl(siteURL, types.PolicyTypeKs3, uploadSession.Props.UploadSessionID, uploadSession.CallbackSecret).String()

	mimeType := file.Props.MimeType
	if mimeType == "" {
		mimeType = handler.mime.TypeByName(file.Props.Uri.Name())
	}

	// create the multipart upload
	res, err := handler.svc.CreateMultipartUploadWithContext(ctx, &s3.CreateMultipartUploadInput{
		Bucket:      &handler.policy.BucketName,
		Key:         &uploadSession.Props.SavePath,
		Expires:     &uploadSession.Props.ExpireAt,
		ContentType: aws.String(mimeType),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create multipart upload: %w", err)
	}

	uploadSession.UploadID = *res.UploadID

	// sign an upload URL for each part
	chunks := chunk.NewChunkGroup(file, handler.chunkSize, &backoff.ConstantBackoff{}, false, handler.l, "")
	urls := make([]string, chunks.Num())
	for chunks.Next() {
		err := chunks.Process(func(c *chunk.ChunkGroup, chunk io.Reader) error {
			// calculate the expiry (seconds)
			expireSeconds := int(time.Until(uploadSession.Props.ExpireAt).Seconds())
			partNumber := c.Index() + 1

			// generate the presigned URL
			signedURL, err := handler.svc.GeneratePresignedUrl(&s3.GeneratePresignedUrlInput{
				HTTPMethod: s3.PUT,
				Bucket:     &handler.policy.BucketName,
				Key:        &uploadSession.Props.SavePath,
				Expires:    int64(expireSeconds),
				Parameters: map[string]*string{
					"partNumber": aws.String(strconv.Itoa(partNumber)),
					"uploadId":   res.UploadID,
				},
				ContentType: aws.String("application/octet-stream"),
			})
			if err != nil {
				return fmt.Errorf("failed to generate presigned upload url for chunk %d: %w", partNumber, err)
			}
			urls[c.Index()] = signedURL
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	// sign the request URL that completes the multipart upload
	expireSeconds := int(time.Until(uploadSession.Props.ExpireAt).Seconds())
	signedURL, err := handler.svc.GeneratePresignedUrl(&s3.GeneratePresignedUrlInput{
		HTTPMethod: s3.POST,
		Bucket:     &handler.policy.BucketName,
		Key:        &file.Props.SavePath,
		Expires:    int64(expireSeconds),
		Parameters: map[string]*string{
			"uploadId": res.UploadID,
		},
		ContentType: aws.String("application/octet-stream"),
	})
	if err != nil {
		return nil, err
	}

	// generate the upload credential
	return &fs.UploadCredential{
		UploadID:    *res.UploadID,
		UploadURLs:  urls,
		CompleteURL: signedURL,
		SessionID:   uploadSession.Props.UploadSessionID,
		ChunkSize:   handler.chunkSize,
	}, nil
}

// CancelToken cancels the upload credential
func (handler *Driver) CancelToken(ctx context.Context, uploadSession *fs.UploadSession) error {
	_, err := handler.svc.AbortMultipartUploadWithContext(ctx, &s3.AbortMultipartUploadInput{
		UploadID: &uploadSession.UploadID,
		Bucket:   &handler.policy.BucketName,
		Key:      &uploadSession.Props.SavePath,
	})
	return err
}

// cancelUpload cancels the multipart upload
func (handler *Driver) cancelUpload(key, id *string) {
	if _, err := handler.svc.AbortMultipartUpload(&s3.AbortMultipartUploadInput{
		Bucket:   &handler.policy.BucketName,
		UploadID: id,
		Key:      key,
	}); err != nil {
		handler.l.Warning("failed to abort multipart upload: %s", err)
	}
}

// Capabilities gets storage capabilities
func (handler *Driver) Capabilities() *driver.Capabilities {
	return &driver.Capabilities{
		StaticFeatures:  features,
		MediaMetaProxy:  handler.policy.Settings.MediaMetaGeneratorProxy,
		ThumbProxy:      handler.policy.Settings.ThumbGeneratorProxy,
		MaxSourceExpire: time.Duration(604800) * time.Second,
	}
}

// MediaMeta gets media metadata
func (handler *Driver) MediaMeta(ctx context.Context, path, ext, language string) ([]driver.MediaMeta, error) {
	return nil, errors.New("not implemented")
}

// LocalPath gets the local path
func (handler *Driver) LocalPath(ctx context.Context, path string) string {
	return ""
}

// CompleteUpload completes the upload
func (handler *Driver) CompleteUpload(ctx context.Context, session *fs.UploadSession) error {
	if session.SentinelTaskID == 0 {
		return nil
	}

	// Make sure uploaded file size is correct
	res, err := handler.Meta(ctx, session.Props.SavePath)
	if err != nil {
		return fmt.Errorf("failed to get uploaded file size: %w", err)
	}

	if res.Size != session.Props.Size {
		return serializer.NewError(
			serializer.CodeMetaMismatch,
			fmt.Sprintf("File size not match, expected: %d, actual: %d", session.Props.Size, res.Size),
			nil,
		)
	}
	return nil
}

// Meta gets file metadata
func (handler *Driver) Meta(ctx context.Context, path string) (*MetaData, error) {
	res, err := handler.svc.HeadObjectWithContext(ctx,
		&s3.HeadObjectInput{
			Bucket: &handler.policy.BucketName,
			Key:    &path,
		})

	if err != nil {
		return nil, err
	}

	return &MetaData{
		Size: *res.ContentLength,
		Etag: *res.ETag,
	}, nil

}

// CORS sets CORS rules
func (handler *Driver) CORS() error {
	rule := s3.CORSRule{
		AllowedMethod: []string{
			"GET",
			"POST",
			"PUT",
			"DELETE",
			"HEAD",
		},
		AllowedOrigin: []string{"*"},
		AllowedHeader: []string{"*"},
		ExposeHeader:  []string{"ETag"},
		MaxAgeSeconds: 3600,
	}

	_, err := handler.svc.PutBucketCORS(&s3.PutBucketCORSInput{
		Bucket: &handler.policy.BucketName,
		CORSConfiguration: &s3.CORSConfiguration{
			Rules: []*s3.CORSRule{&rule},
		},
	})

	return err
}

// Reader wraps a reader
type Reader struct {
	r io.Reader
}

// Read reads data
func (r Reader) Read(p []byte) (int, error) {
	return r.r.Read(p)
}
