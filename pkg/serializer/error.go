package serializer

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/lock"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
)

// AppError application error, implements the error interface
type AppError struct {
	Code     int
	Msg      string
	RawError error
}

// NewError returns a new error object
func NewError(code int, msg string, err error) AppError {
	return AppError{
		Code:     code,
		Msg:      msg,
		RawError: err,
	}
}

// NewErrorFromResponse builds an error from serializer.Response
func NewErrorFromResponse(resp *Response) AppError {
	return AppError{
		Code:     resp.Code,
		Msg:      resp.Msg,
		RawError: errors.New(resp.Error),
	}
}

// WithError wraps a standard library error into the app error
func (err *AppError) WithError(raw error) AppError {
	return AppError{
		Code:     err.Code,
		Msg:      err.Msg,
		RawError: raw,
	}
}

// Error returns the readable error message set by the business code
func (err AppError) Error() string {
	if err.RawError != nil {
		return fmt.Sprintf("%s: %s", err.Msg, err.RawError.Error())
	}
	return err.Msg
}

func (err AppError) ErrCode() int {
	var inheritedErr AppError
	if errors.As(err.RawError, &inheritedErr) {
		return inheritedErr.ErrCode()
	}
	return err.Code
}

func (err AppError) Unwrap() error {
	return err.RawError
}

// Three-digit error codes reuse the original HTTP meaning
// Five-digit error codes are app-defined errors
// Five-digit codes starting with 5 are server-side errors, e.g. a failed database operation
// Five-digit codes starting with 4 are client errors, caused by a client bug or a user mistake
const (
	// CodeNotFullySuccess not fully successful
	CodeNotFullySuccess = 203
	// CodeCheckLogin not logged in
	CodeCheckLogin = 401
	// CodeNoPermissionErr unauthorized access
	CodeNoPermissionErr = 403
	// CodeNotFound resource not found
	CodeNotFound = 404
	// CodeConflict resource conflict
	CodeConflict = 409
	// CodeUploadFailed upload failed
	CodeUploadFailed = 40002
	// CodeCreateFolderFailed failed to create folder
	CodeCreateFolderFailed = 40003
	// CodeObjectExist object already exists
	CodeObjectExist = 40004
	// CodeSignExpired signature expired
	CodeSignExpired = 40005
	// CodePolicyNotAllowed not allowed by the current storage policy
	CodePolicyNotAllowed = 40006
	// CodeGroupNotAllowed the user group cannot do this
	CodeGroupNotAllowed = 40007
	// CodeAdminRequired not an admin group
	CodeAdminRequired = 40008
	// CodeMasterNotFound master node not registered
	CodeMasterNotFound = 40009
	// CodeUploadSessionExpired upload session expired
	CodeUploadSessionExpired = 40011
	// CodeInvalidChunkIndex invalid chunk index
	CodeInvalidChunkIndex = 40012
	// CodeInvalidContentLength invalid content length
	CodeInvalidContentLength = 40013
	// CodePhoneRequired no phone number bound
	CodePhoneRequired = 40010
	// CodeBatchSourceSize exceeds the batch limit for direct links
	CodeBatchSourceSize = 40014
	// CodeBatchAria2Size exceeds the maximum number of Aria2 tasks
	CodeBatchAria2Size = 40015
	// CodeParentNotExist parent folder does not exist
	CodeParentNotExist = 40016
	// CodeUserBaned user is not active
	CodeUserBaned = 40017
	// CodeUserNotActivated user is not active
	CodeUserNotActivated = 40018
	// CodeFeatureNotEnabled this feature is not enabled
	CodeFeatureNotEnabled = 40019
	// CodeCredentialInvalid invalid credential
	CodeCredentialInvalid = 40020
	// CodeUserNotFound user does not exist
	CodeUserNotFound = 40021
	// Code2FACodeErr wrong two-factor code
	Code2FACodeErr = 40022
	// CodeLoginSessionNotExist login session does not exist
	CodeLoginSessionNotExist = 40023
	// CodeInitializeAuthn cannot initialize WebAuthn
	CodeInitializeAuthn = 40024
	// CodeWebAuthnCredentialError WebAuthn invalid credential
	CodeWebAuthnCredentialError = 40025
	// CodeCaptchaError wrong captcha
	CodeCaptchaError = 40026
	// CodeCaptchaRefreshNeeded captcha needs a refresh
	CodeCaptchaRefreshNeeded = 40027
	// CodeFailedSendEmail failed to send email
	CodeFailedSendEmail = 40028
	// CodeInvalidTempLink invalid temporary link
	CodeInvalidTempLink = 40029
	// CodeTempLinkExpired temporary link expired
	CodeTempLinkExpired = 40030
	// CodeEmailProviderBaned email domain is banned
	CodeEmailProviderBaned = 40031
	// CodeEmailExisted email already in use
	CodeEmailExisted = 40032
	// CodeEmailSent email was sent again
	CodeEmailSent = 40033
	// CodeUserCannotActivate user cannot be activated
	CodeUserCannotActivate = 40034
	// storage policy does not exist
	CodePolicyNotExist = 40035
	// cannot delete the default storage policy
	CodeDeleteDefaultPolicy = 40036
	// storage policy still has files
	CodePolicyUsedByFiles = 40037
	// storage policy is bound to a user group
	CodePolicyUsedByGroups = 40038
	// user group does not exist
	CodeGroupNotFound = 40039
	// illegal operation on a system user group
	CodeInvalidActionOnSystemGroup = 40040
	// user group is in use
	CodeGroupUsedByUser = 40041
	// changing the group of the initial user
	CodeChangeGroupForDefaultUser = 40042
	// illegal operation on a system user
	CodeInvalidActionOnDefaultUser = 40043
	// file does not exist
	CodeFileNotFound = 40044
	// failed to list files
	CodeListFilesError = 40045
	// illegal operation on a system node
	CodeInvalidActionOnSystemNode = 40046
	// failed to create file system
	CodeCreateFSError = 40047
	// failed to create task
	CodeCreateTaskError = 40048
	// file is too large
	CodeFileTooLarge = 40049
	// file type not allowed
	CodeFileTypeNotAllowed = 40050
	// not enough user quota
	CodeInsufficientCapacity = 40051
	// illegal object name
	CodeIllegalObjectName = 40052
	// this operation is not supported on the root folder
	CodeRootProtected = 40053
	// a file with the same name is already being uploaded in this folder
	CodeConflictUploadOngoing = 40054
	// file information mismatch
	CodeMetaMismatch = 40055
	// unsupported archive format
	CodeUnsupportedArchiveType = 40056
	// available storage policies changed
	CodePolicyChanged = 40057
	// invalid share link
	CodeShareLinkNotFound = 40058
	// cannot save your own share
	CodeSaveOwnShare = 40059
	// slave node cannot send callback requests to the master
	CodeSlavePingMaster = 40060
	// app version mismatch
	CodeVersionMismatch = 40061
	// not enough credits
	CodeInsufficientCredit = 40062
	// user group conflict
	CodeGroupConflict = 40063
	// already in this user group
	CodeGroupInvalid = 40064
	// invalid redeem code
	CodeInvalidGiftCode = 40065
	// account already bound
	CodeOpenIDBindConflict = 40066
	// the account is already bound to another account
	CodeOpenIDBindOtherAccount = 40067
	// account not bound
	CodeOpenIDNotLinked = 40068
	// wrong password
	CodeIncorrectPassword = 40069
	// share cannot be previewed
	CodeDisabledSharePreview = 40070
	// invalid signature
	CodeInvalidSign = 40071
	// admins cannot buy user groups
	CodeFulfillAdminGroup = 40072
	// Lock confliced
	CodeLockConflict = 40073
	// Too many uris
	CodeTooManyUris = 40074
	// Lock token expired
	CodeLockExpired = 40075
	// Current updated version is stale
	CodeStaleVersion = 40076
	// CodeEntityNotExist Entity not exist
	CodeEntityNotExist = 40077
	// CodeFileDeleted File is deleted in recycle bin
	CodeFileDeleted = 40078
	// CodeFileCountLimitedReached file count limited reached
	CodeFileCountLimitedReached = 40079
	// CodeInvalidPassword invalid password
	CodeInvalidPassword = 40080
	// CodeBatchOperationNotFullyCompleted batch operation not fully completed
	CodeBatchOperationNotFullyCompleted = 40081
	// CodeOwnerOnly owner operation only
	CodeOwnerOnly = 40082
	// CodePurchaseRequired purchase required
	CodePurchaseRequired = 40083
	// CodeManagedAccountMinimumOpenID managed account minimum openid
	CodeManagedAccountMinimumOpenID = 40084
	// CodeAmountTooSmall amount too small
	CodeAmountTooSmall = 40085
	// CodeNodeUsedByStoragePolicy node used by storage policy
	CodeNodeUsedByStoragePolicy = 40086
	// CodeAnonymouseAccessDenied anonymous users cannot access the share
	CodeAnonymouseAccessDenied = 40088
	// CodeInsufficientScope OAuth token scope insufficient
	CodeInsufficientScope = 40089
	// CodeDBError database operation failed
	CodeDBError = 50001
	// CodeEncryptError encryption failed
	CodeEncryptError = 50002
	// CodeIOFailed IO operation failed
	CodeIOFailed = 50004
	// CodeInternalSetting invalid internal setting parameter
	CodeInternalSetting = 50005
	// CodeCacheOperation cache operation failed
	CodeCacheOperation = 50006
	// CodeCallbackError callback failed
	CodeCallbackError = 50007
	// failed to update admin settings
	CodeUpdateSetting = 50008
	// failed to add CORS policy
	CodeAddCORS = 50009
	// node unavailable
	CodeNodeOffline = 50010
	// failed to query file metadata
	CodeQueryMetaFailed = 50011
	//CodeParamErr various odd parameter errors
	CodeParamErr = 40001
	// CodeNotSet undetermined error, taken from the error later if possible
	CodeNotSet = -1
)

// DBErrDeprecated database operation failed
func DBErr(c context.Context, msg string, err error) Response {
	if msg == "" {
		msg = "Database operation failed."
	}
	return ErrWithDetails(c, CodeDBError, msg, err)
}

// DBErrDeprecated database operation failed
func DBErrDeprecated(msg string, err error) Response {
	if msg == "" {
		msg = "Database operation failed."
	}
	return ErrDeprecated(CodeDBError, msg, err)
}

// ParamErr various parameter errors
func ParamErr(c context.Context, msg string, err error) Response {
	if msg == "" {
		msg = "Invalid parameters."
	}
	return ErrWithDetails(c, CodeParamErr, msg, err)
}

// ParamErrDeprecated various parameter errors
// Deprecated
func ParamErrDeprecated(msg string, err error) Response {
	if msg == "" {
		msg = "Invalid parameters."
	}
	return ErrDeprecated(CodeParamErr, msg, err)
}

// ErrDeprecated generic error handling
func ErrDeprecated(errCode int, msg string, err error) Response {
	// if the underlying error is an AppError, try to get details from it
	var appError AppError
	if errors.As(err, &appError) {
		errCode = appError.Code
		err = appError.RawError
		msg = appError.Msg
	}

	res := Response{
		Code: errCode,
		Msg:  msg,
	}
	// hide the underlying error in production
	if err != nil && gin.Mode() != gin.ReleaseMode {
		res.Error = err.Error()
	}
	return res
}

// ErrWithDetails generic error handling
func ErrWithDetails(c context.Context, errCode int, msg string, err error) Response {
	res := Response{
		Code:          errCode,
		Msg:           msg,
		CorrelationID: logging.CorrelationID(c).String(),
	}

	// if the underlying error is an AppError, try to get details from it
	var appError AppError
	if errors.As(err, &appError) {
		res.Code = appError.ErrCode()
		err = appError.RawError
		res.Msg = appError.Msg

		// Special case for error with detail data
		switch res.Code {
		case CodeLockConflict:
			var lockConflict lock.ConflictError
			if errors.As(err, &lockConflict) {
				res.Data = lockConflict
			}
		case CodeBatchOperationNotFullyCompleted:
			var errs *AggregateError
			if errors.As(err, &errs) {
				res.AggregatedError = errs.Expand(c)
			}
		}

		// Fylvo: an error that knows data for the client carries it in every answer,
		// also as one entry of an aggregated error.
		var detailed interface{ ResponseData() any }
		if errors.As(err, &detailed) {
			res.Data = detailed.ResponseData()
		}
	}

	// hide the underlying error in production
	if err != nil && gin.Mode() != gin.ReleaseMode {
		res.Error = err.Error()
	}

	return res
}

// Err Builds error response without addition details, code and message will
// be retrieved from error if possible
func Err(c context.Context, err error) Response {
	return ErrWithDetails(c, CodeNotSet, "", err)
}

// AggregateError is a special error type that contains multiple errors
type AggregateError struct {
	errs map[string]error
}

// NewAggregateError creates a new AggregateError
func NewAggregateError() *AggregateError {
	return &AggregateError{
		errs: make(map[string]error, 0),
	}
}

func (e *AggregateError) Error() string {
	return fmt.Sprintf("aggregate error: one or more operation failed")
}

// Add adds an error to the aggregate
func (e *AggregateError) Add(id string, err error) {
	e.errs[id] = err
}

// Merge merges another aggregate error into this one
func (e *AggregateError) Merge(err error) bool {
	var errs *AggregateError
	if errors.As(err, &errs) {
		for id, err := range errs.errs {
			e.errs[id] = err
		}

		return true
	}
	return false
}

// Raw returns the raw error map
func (e *AggregateError) Raw() map[string]error {
	return e.errs
}

func (e *AggregateError) Remove(id string) {
	delete(e.errs, id)
}

// Expand expands the aggregate error into a list of responses
func (e *AggregateError) Expand(ctx context.Context) map[string]Response {
	return lo.MapEntries(e.errs, func(id string, err error) (string, Response) {
		return id, Err(ctx, err)
	})
}

// Aggregate aggregates the error and returns nil if there is no error;
// otherwise returns the error itself
func (e *AggregateError) Aggregate() error {
	if len(e.errs) == 0 {
		return nil
	}

	msg := "One or more operation failed"
	if len(e.errs) == 1 {
		for _, err := range e.errs {
			msg = err.Error()
		}
	}

	return NewError(CodeBatchOperationNotFullyCompleted, msg, e)
}

func (e *AggregateError) FormatFirstN(n int) string {
	if len(e.errs) == 0 {
		return ""
	}

	res := make([]string, 0, n)
	for id, err := range e.errs {
		res = append(res, fmt.Sprintf("%s: %s", id, err.Error()))
		if len(res) >= n {
			break
		}
	}

	return strings.Join(res, ", ")

}
