import { StoragePolicy } from "../../../../api/explorer.ts";
import { AppError, Response } from "../../../../api/request.ts";
import i18next from "../../../../i18n";
import { sizeToString } from "../../../../util";
import { OneDriveError, QiniuError, UpyunError } from "../types";

export enum UploaderErrorName {
  InvalidFile = "InvalidFile",
  NoPolicySelected = "NoPolicySelected",
  UnknownPolicyType = "UnknownPolicyType",
  FailedCreateUploadSession = "FailedCreateUploadSession",
  FailedDeleteUploadSession = "FailedDeleteUploadSession",
  HTTPRequestFailed = "HTTPRequestFailed",
  LocalChunkUploadFailed = "LocalChunkUploadFailed",
  SlaveChunkUploadFailed = "SlaveChunkUploadFailed",
  WriteCtxFailed = "WriteCtxFailed",
  RemoveCtxFailed = "RemoveCtxFailed",
  ReadCtxFailed = "ReadCtxFailed",
  InvalidCtxData = "InvalidCtxData",
  CtxExpired = "CtxExpired",
  ProcessingTaskDuplicated = "ProcessingTaskDuplicated",
  OneDriveChunkUploadFailed = "OneDriveChunkUploadFailed",
  OneDriveEmptyFile = "OneDriveEmptyFile",
  FailedFinishOneDriveUpload = "FailedFinishOneDriveUpload",
  S3LikeChunkUploadFailed = "S3LikeChunkUploadFailed",
  S3LikeUploadCallbackFailed = "S3LikeUploadCallbackFailed",
  COSUploadCallbackFailed = "COSUploadCallbackFailed",
  COSPostUploadFailed = "COSPostUploadFailed",
  UpyunPostUploadFailed = "UpyunPostUploadFailed",
  QiniuChunkUploadFailed = "QiniuChunkUploadFailed",
  FailedFinishOSSUpload = "FailedFinishOSSUpload",
  FailedFinishQiniuUpload = "FailedFinishQiniuUpload",
  FailedTransformResponse = "FailedTransformResponse",
  LoadBalancePolicyNoAvailable = "LoadBalancePolicyNoAvailable",
}

const RETRY_ERROR_LIST = [
  UploaderErrorName.FailedCreateUploadSession,
  UploaderErrorName.HTTPRequestFailed,
  UploaderErrorName.LocalChunkUploadFailed,
  UploaderErrorName.SlaveChunkUploadFailed,
  UploaderErrorName.ProcessingTaskDuplicated,
  UploaderErrorName.FailedTransformResponse,
];

const RETRY_CODE_LIST = [-1];
const CONFLICT_ERROR_CODE = 40004;

export class UploaderError implements Error {
  public stack: string | undefined;
  constructor(
    public name: UploaderErrorName,
    public message: string,
  ) {
    this.stack = new Error().stack;
  }

  public Message(): string {
    return this.message;
  }

  public Retryable(): boolean {
    return RETRY_ERROR_LIST.includes(this.name);
  }
}

// file failed storage policy validation
export class FileValidateError extends UploaderError {
  // file property that failed validation
  public field: "size" | "suffix" | "suffix_denied" | "regexp";

  // the matching storage policy
  public policy: StoragePolicy;

  constructor(message: string, field: "size" | "suffix" | "suffix_denied" | "regexp", policy: StoragePolicy) {
    super(UploaderErrorName.InvalidFile, message);
    this.field = field;
    this.policy = policy;
  }

  public Message(): string {
    if (this.field == "size") {
      return i18next.t(`uploader.sizeExceedLimitError`, {
        max: sizeToString(this.policy.max_size),
      });
    }

    if (this.field == "suffix_denied") {
      return (
        i18next.t("uploader.suffixNotAllowedError") +
        i18next.t(`uploader.suffixDenied`, {
          denied: this.policy.denied_suffix ? this.policy.denied_suffix.join(",") : "*",
        })
      );
    }

    if (this.field == "regexp") {
      return i18next.t("uploader.regexpNotAllowedError");
    }

    return (
      i18next.t(`uploader.suffixNotAllowedError`) +
      i18next.t(`uploader.suffixAllowed`, {
        supported: this.policy.allowed_suffix ? this.policy.allowed_suffix.join(",") : "*",
      })
    );
  }
}

// unknown storage policy
export class UnknownPolicyError extends UploaderError {
  // the matching storage policy
  public policy: StoragePolicy;

  constructor(message: string, policy: StoragePolicy) {
    super(UploaderErrorName.UnknownPolicyType, message);
    this.policy = policy;
  }
}

// backend API error
export class APIError extends UploaderError {
  private appError: AppError;
  constructor(
    name: UploaderErrorName,
    message: string,
    protected response: Response<any>,
  ) {
    super(name, message);
    this.appError = new AppError(response);
  }

  public Message(): string {
    return `${this.message}: ${this.appError.message}`;
  }

  public Retryable(): boolean {
    return super.Retryable() && RETRY_CODE_LIST.includes(this.response.code);
  }

  public IsConflictError(): boolean {
    return this.response.code == CONFLICT_ERROR_CODE;
  }
}

// cannot create upload session
export class CreateUploadSessionError extends APIError {
  constructor(response: Response<any>) {
    super(UploaderErrorName.FailedCreateUploadSession, "", response);
  }

  public Message(): string {
    this.message = i18next.t(`uploader.createUploadSessionError`);
    return super.Message();
  }
}

// cannot delete upload session
export class DeleteUploadSessionError extends APIError {
  constructor(response: Response<any>) {
    super(UploaderErrorName.FailedDeleteUploadSession, "", response);
  }

  public Message(): string {
    this.message = i18next.t(`uploader.deleteUploadSessionError`);
    return super.Message();
  }
}

// HTTP request error
export class HTTPError extends UploaderError {
  public response?: any;
  constructor(
    public axiosErr: any,
    protected url: string,
  ) {
    super(UploaderErrorName.HTTPRequestFailed, axiosErr.message);
    this.response = axiosErr.response;
  }

  public Message(): string {
    return i18next.t(`uploader.requestError`, {
      msg: this.axiosErr,
      url: this.url,
    });
  }
}

// local chunk upload failed
export class LocalChunkUploadError extends APIError {
  constructor(
    response: Response<any>,
    protected chunkIndex: number,
  ) {
    super(UploaderErrorName.LocalChunkUploadFailed, "", response);
  }

  public Message(): string {
    this.message = i18next.t(`uploader.chunkUploadError`, {
      index: this.chunkIndex,
    });
    return super.Message();
  }
}

// slave chunk upload failed
export class SlaveChunkUploadError extends APIError {
  constructor(
    response: Response<any>,
    protected chunkIndex: number,
  ) {
    super(UploaderErrorName.SlaveChunkUploadFailed, "", response);
  }

  public Message(): string {
    this.message = i18next.t(`uploader.chunkUploadError`, {
      index: this.chunkIndex,
    });
    return super.Message();
  }
}

// upload task conflict
export class ProcessingTaskDuplicatedError extends UploaderError {
  constructor() {
    super(UploaderErrorName.ProcessingTaskDuplicated, "Processing task duplicated");
  }

  public Message(): string {
    return i18next.t(`uploader.conflictError`);
  }
}

// OneDrive chunk upload failed
export class OneDriveChunkError extends UploaderError {
  constructor(public response: OneDriveError) {
    super(UploaderErrorName.OneDriveChunkUploadFailed, response.error.message);
  }

  public Message(): string {
    let msg = i18next.t(`uploader.chunkUploadErrorWithMsg`, {
      msg: this.message,
    });

    if (this.response.error.retryAfterSeconds != undefined) {
      msg +=
        " " +
        i18next.t(`uploader.chunkUploadErrorWithRetryAfter`, {
          retryAfter: this.response.error.retryAfterSeconds,
        });
    }

    return msg;
  }

  public Retryable(): boolean {
    return super.Retryable() || this.response.error.retryAfterSeconds != undefined;
  }
}

// OneDrive empty file selected for upload
export class OneDriveEmptyFileSelected extends UploaderError {
  constructor() {
    super(UploaderErrorName.OneDriveEmptyFile, "empty file not supported");
  }

  public Message(): string {
    return i18next.t("uploader.emptyFileError");
  }
}

// OneDrive cannot complete the file upload
export class OneDriveFinishUploadError extends APIError {
  constructor(response: Response<any>) {
    super(UploaderErrorName.FailedFinishOneDriveUpload, "", response);
  }

  public Message(): string {
    this.message = i18next.t("uploader.finishUploadError");
    return super.Message();
  }
}

// S3 policy chunk upload failed
export class S3LikeChunkError extends UploaderError {
  constructor(public response: Document) {
    super(UploaderErrorName.S3LikeChunkUploadFailed, response.getElementsByTagName("Message")[0].innerHTML);
  }

  public Message(): string {
    return i18next.t(`uploader.chunkUploadErrorWithMsg`, {
      msg: this.message,
    });
  }
}

// OSS upload completion failed
export class S3LikeFinishUploadError extends UploaderError {
  constructor(public response: Document) {
    super(UploaderErrorName.S3LikeChunkUploadFailed, response.getElementsByTagName("Message")[0].innerHTML);
  }

  public Message(): string {
    return i18next.t(`uploader.ossFinishUploadError`, {
      msg: this.message,
      code: this.response.getElementsByTagName("Code")[0].innerHTML,
    });
  }
}

export interface ObsJsonError {
  message: string;
  code: string;
}

export class ObsFinishUploadError extends UploaderError {
  constructor(public response: ObsJsonError) {
    super(UploaderErrorName.S3LikeChunkUploadFailed, response.message);
  }

  public Message(): string {
    return i18next.t(`uploader.ossFinishUploadError`, {
      msg: this.message,
      code: this.response.code,
    });
  }
}

// qiniu chunk upload failed
export class QiniuChunkError extends UploaderError {
  constructor(public response: QiniuError) {
    super(UploaderErrorName.QiniuChunkUploadFailed, response.error);
  }

  public Message(): string {
    return i18next.t(`uploader.chunkUploadErrorWithMsg`, {
      msg: this.message,
    });
  }
}

// qiniu upload completion failed
export class QiniuFinishUploadError extends UploaderError {
  constructor(public response: QiniuError) {
    super(UploaderErrorName.FailedFinishQiniuUpload, response.error);
  }

  public Message(): string {
    return i18next.t(`uploader.finishUploadErrorWithMsg`, {
      msg: this.message,
    });
  }
}

// COS upload failed
export class COSUploadError extends UploaderError {
  constructor(public response: Document) {
    super(UploaderErrorName.COSPostUploadFailed, response.getElementsByTagName("Message")[0].innerHTML);
  }

  public Message(): string {
    return i18next.t(`uploader.cosUploadFailed`, {
      msg: this.message,
      code: this.response.getElementsByTagName("Code")[0].innerHTML,
    });
  }
}

// COS cannot complete the upload callback
export class COSUploadCallbackError extends APIError {
  constructor(response: Response<any>) {
    super(UploaderErrorName.COSUploadCallbackFailed, "", response);
  }

  public Message(): string {
    this.message = i18next.t("uploader.finishUploadError");
    return super.Message();
  }
}

// Upyun upload failed
export class UpyunUploadError extends UploaderError {
  constructor(public response: UpyunError) {
    super(UploaderErrorName.UpyunPostUploadFailed, response.message);
  }

  public Message(): string {
    return i18next.t("uploader.upyunUploadFailed", {
      msg: this.message,
    });
  }
}

// S3 cannot complete the upload callback
export class S3LikeUploadCallbackError extends APIError {
  constructor(response: Response<any>) {
    super(UploaderErrorName.S3LikeUploadCallbackFailed, "", response);
  }

  public Message(): string {
    this.message = i18next.t("uploader.finishUploadError");
    return super.Message();
  }
}

// cannot parse the response
export class TransformResponseError extends UploaderError {
  constructor(
    private response: string,
    parseError: Error,
  ) {
    super(UploaderErrorName.FailedTransformResponse, parseError.message);
  }

  public Message(): string {
    return i18next.t("uploader.parseResponseError", {
      msg: this.message,
      content: this.response,
    });
  }
}
