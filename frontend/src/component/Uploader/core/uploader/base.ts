// 所有 Uploader 的基类
import axios, { CanceledError, CancelTokenSource } from "axios";
import { EncryptionCipher, PolicyType } from "../../../../api/explorer.ts";
import CrUri from "../../../../util/uri.ts";
import { createUploadSession, deleteUploadSession } from "../api";
import { UploaderError } from "../errors";
import UploadManager from "../index";
import Logger from "../logger";
import { Task } from "../types";
import * as utils from "../utils";
import { CancelToken } from "../utils/request";
import { validate } from "../utils/validator";
import { EncryptedBlob } from "./encrypt/blob.ts";
import { inConnectedStorage } from "../../../../api/connected.ts";

export enum Status {
  added,
  resumable,
  initialized,
  queued,
  preparing,
  processing,
  finishing,
  finished,
  error,
  canceled,
}

export interface UploadHandlers {
  onTransition: (newStatus: Status) => void;
  onError: (err: Error) => void;
  onProgress: (data: UploadProgress) => void;
  onMsg: (msg: string, color: MessageColor) => void;
}
export type MessageColor = "error" | "default" | "success" | "warning" | "info" | "loading" | undefined;

export interface UploadProgress {
  total: ProgressCompose;
  chunks?: ProgressCompose[];
}

export interface ProgressCompose {
  size: number;
  loaded: number;
  percent: number;
  fromCache?: boolean;
}

export interface Progress {
  total?: number;
  loaded: number;
}

export interface RetryOption {
  overwrite?: boolean;
  new_prefix?: string;
}

const resumePolicy = [
  PolicyType.local,
  PolicyType.remote,
  PolicyType.qiniu,
  PolicyType.oss,
  PolicyType.onedrive,
  PolicyType.s3,
  PolicyType.ks3,
];
const deleteUploadSessionDelay = 500;

export const uploadPromisePool: {
  [key: string]: {
    resolve: (value: Task | PromiseLike<Task>) => void;
    reject: (reason?: any) => void;
  };
} = {};

// Fylvo: uploads into connected storages go at most two at a time; the server talks to the
// provider with at most two requests per account.
const connectedSlots = { free: 2, waiting: [] as (() => void)[] };
const takeConnectedSlot = async () => {
  if (connectedSlots.free > 0) {
    connectedSlots.free--;
    return;
  }
  await new Promise<void>((resolve) => connectedSlots.waiting.push(resolve));
};
const giveConnectedSlot = () => {
  const next = connectedSlots.waiting.shift();
  if (next) {
    next();
  } else {
    connectedSlots.free++;
  }
};

export default abstract class Base {
  public child?: Base[];
  public status: Status = Status.added;
  public error?: Error;
  public progress?: UploadProgress;

  public id = ++Base.id;
  private static id = 0;

  protected logger: Logger;
  protected subscriber: UploadHandlers;
  // 用于取消请求
  protected cancelToken: CancelTokenSource = CancelToken.source();
  // Fylvo: set by the user's cancel, apart from the token the chunk pool also cancels on errors.
  protected canceledByUser = false;

  public lastTime = Date.now();
  public startTime = Date.now();
  public promiseId: string | undefined;

  constructor(
    public task: Task,
    protected manager: UploadManager,
  ) {
    this.logger = new Logger(this.manager.logger.level, "UPLOADER", this.id);
    this.logger.info("Initialize new uploader for task: ", task);
    this.subscriber = {
      /* eslint-disable @typescript-eslint/no-empty-function */
      onTransition: (_newStatus: Status) => {},
      onError: (_err: Error) => {},
      onProgress: (_data: UploadProgress) => {},
      onMsg: (_msg, _color: MessageColor) => {},
      /* eslint-enable @typescript-eslint/no-empty-function */
    };
  }

  public subscribe = (handlers: UploadHandlers) => {
    this.subscriber = handlers;
  };

  public start = async () => {
    this.logger.info("Activate uploading task");
    this.transit(Status.initialized);
    this.lastTime = this.startTime = Date.now();

    try {
      validate(this.task.file, this.task.policy);
    } catch (e) {
      this.logger.error("File validate failed with error:", e);
      this.setError(e as Error);
      return;
    }

    this.logger.info("Enqueued in manager pool");
    this.transit(Status.queued);
    this.manager.pool.enqueue(this).catch((e) => {
      this.logger.info("Upload task failed with error:", e);
      this.setError(e);
    });
  };

  public run = async () => {
    if (!inConnectedStorage(this.task.dst)) {
      return this.runTask();
    }
    // Fylvo: the place is taken before the session is opened, and kept until the end.
    await takeConnectedSlot();
    try {
      await this.runTask();
    } finally {
      giveConnectedSlot();
    }
  };

  private runTask = async () => {
    this.logger.info("Start upload task, create upload session...");
    this.transit(Status.preparing);
    const cachedInfo = utils.getResumeCtx(this.task, this.logger);
    if (cachedInfo == null) {
      const crUri = new CrUri(this.task.dst);
      crUri.join(this.task.name);
      this.task.session = await createUploadSession(
        {
          uri: crUri.toString(),
          size: this.task.file.size,
          policy_id: this.task.policy.id,
          last_modified: this.task.file.lastModified,
          mime_type: this.task.file.type,
          entity_type: this.task.overwrite ? "version" : undefined,
          encryption_supported:
            this.task.policy.encryption && "crypto" in window ? [EncryptionCipher.aes256ctr] : undefined,
        },
        this.cancelToken.token,
      );
      this.logger.info("Upload session created:", this.task.session);
    } else {
      this.task.session = cachedInfo.session;
      this.task.resumed = true;
      this.task.chunkProgress = cachedInfo.chunkProgress;
      this.logger.info("Resume upload from cached ctx:", cachedInfo);
    }

    if (this.task.session?.encrypt_metadata && !this.task.policy?.relay) {
      // Check browser support for encryption
      if (!("crypto" in window)) {
        this.logger.error("Encryption is not supported in this browser");
        this.setError(new Error("Web Crypto API is not supported in this browser"));
        return;
      }

      const encryptedBlob = new EncryptedBlob(this.task.file, this.task.session?.encrypt_metadata);
      this.task.blob = encryptedBlob;
    } else {
      this.task.blob = this.task.file;
    }

    this.transit(Status.processing);
    await this.upload();
    await this.afterUpload();
    utils.removeResumeCtx(this.task, this.logger);
    this.transit(Status.finished);
    this.logger.info("Upload task completed");
  };

  public abstract upload(): Promise<any>;
  protected async afterUpload(): Promise<any> {
    return;
  }

  public cancel = async () => {
    if (this.status === Status.finished) {
      return;
    }

    this.canceledByUser = true;
    this.cancelToken.cancel();
    await this.cancelUploadSession();
    this.transit(Status.canceled);
  };

  public retry = (opt?: RetryOption) => {
    this.reset();
    if (opt?.overwrite) {
      this.task.overwrite = true;
    } else if (opt?.new_prefix) {
      this.task.name = opt.new_prefix + this.task.name;
    }
    this.start();
  };

  protected reset = () => {
    this.canceledByUser = false;
    this.cancelToken = axios.CancelToken.source();
    this.progress = {
      total: {
        size: 0,
        loaded: 0,
        percent: 0,
      },
    };
  };

  protected setError(e: Error) {
    if (e instanceof CanceledError) {
      return;
    }

    if (!(e instanceof UploaderError && e.Retryable()) || !resumePolicy.includes(this.task.policy.type)) {
      this.logger.warn("Non-resume error occurs, clean resume ctx cache");
      this.cancelUploadSession();
    }

    this.status = Status.error;
    this.error = e;
    this.subscriber.onError(e);
  }

  protected cancelUploadSession = (): Promise<void> => {
    return new Promise<void>((resolve) => {
      utils.removeResumeCtx(this.task, this.logger);
      if (this.task.session) {
        setTimeout(() => {
          deleteUploadSession(this.task.session!?.session_id, this.task.session!?.uri)
            .catch((e) => {
              this.logger.warn("Failed to cancel upload session: ", e);
            })
            .finally(() => {
              resolve();
            });
        }, deleteUploadSessionDelay);
      } else {
        resolve();
      }
    });
  };

  protected transit(status: Status) {
    this.status = status;
    if (this.promiseId && status === Status.finished) {
      const promise = uploadPromisePool[this.promiseId];
      delete uploadPromisePool[this.promiseId];
      this.promiseId = undefined;
      if (promise) {
        switch (status) {
          case Status.finished:
            promise.resolve(this.task);
            break;
          default:
            promise.reject(this.error);
            break;
        }
      }
    }
    this.subscriber.onTransition(status);
  }

  public getProgressInfoItem(loaded: number, size: number, fromCache?: boolean): ProgressCompose {
    return {
      size,
      loaded,
      percent: (loaded / size) * 100,
      ...(fromCache == null ? {} : { fromCache }),
    };
  }

  public key(): string {
    return utils.getResumeCtxKey(this.task);
  }
}
