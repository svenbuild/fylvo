import * as utils from "../utils";
import Base from "./base";
import { EncryptedBlob } from "./encrypt/blob";
import { getConnectedUploadState, inConnectedStorage } from "../../../../api/connected.ts";
import { store } from "../../../../redux/store.ts";
import { LocalChunkUploadError } from "../errors";

export interface ChunkProgress {
  loaded: number;
  index: number;
  etag?: string;
}

export interface ChunkInfo {
  chunk: Blob;
  index: number;
}

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

// Fylvo: answers of a connected storage's upload that are handled apart.
const ConnectedBusy = 40923;
const ConnectedOutcomeUnknown = 40927;

export default abstract class Chunk extends Base {
  protected chunks: Blob[] = [];
  private readonly DEFAULT_CONCURRENCY = 1; // Default concurrent uploads
  private readonly MAX_RETRIES = 3;
  private progressUpdateMutex = Promise.resolve(); // Ensure progress updates are serialized
  // Fylvo: busy answers of a connected storage in a row, and the next wait.
  private busyInARow = 0;
  private busyWait = 2000;

  private get connected() {
    return inConnectedStorage(this.task.dst);
  }

  public upload = async () => {
    this.logger.info("Preparing uploading file chunks.");
    this.initBeforeUploadChunks();

    this.logger.info("Starting concurrent uploading of file chunks:", this.chunks);
    this.updateLocalCache();

    if (!this.connected) {
      await this.uploadChunksWithDynamicPool();
      return;
    }
    await takeConnectedSlot();
    try {
      await this.uploadChunksWithDynamicPool();
    } catch (e) {
      // An answer lost on the way does not mean the upload failed: ask where it stands.
      if (await this.connectedUploadDone(e)) {
        return;
      }
      throw e;
    } finally {
      giveConnectedSlot();
    }
  };

  // Fylvo: after a failure without an answer of the server (for example a dropped
  // connection), the state route says whether the upload into a connected storage went
  // through. Anything but "done" is an unknown outcome: the user is told, nothing is sent again.
  private async connectedUploadDone(e: unknown): Promise<boolean> {
    const code = (e as { response?: { code?: number } })?.response?.code;
    const session = this.task.session?.session_id;
    if (code !== undefined || !session || this.cancelToken.token.reason) {
      return false;
    }
    try {
      const state = await store.dispatch(getConnectedUploadState(session));
      if (state.state == "done") {
        return true;
      }
    } catch (_) {
      // The server no longer knows the session (40016 or a restart): unknown as well.
    }
    throw new LocalChunkUploadError(
      { code: ConnectedOutcomeUnknown, msg: "", data: undefined },
      this.chunks.length - 1,
    );
  }

  private async uploadChunksWithDynamicPool() {
    // Get chunks that need to be uploaded
    const chunksToUpload = this.chunks
      .map((chunk, index) => ({ chunk, index }))
      .filter(({ chunk, index }) => this.task.chunkProgress[index].loaded < chunk.size || chunk.size === 0);

    if (chunksToUpload.length === 0) {
      this.logger.info("All chunks already uploaded, skipping.");
      return;
    }

    this.logger.info(`Found ${chunksToUpload.length} chunks to upload out of ${this.chunks.length} total chunks.`);

    const concurrency = this.getConcurrency();
    let chunkIndex = 0;
    let activeCount = 0;
    let hasError = false;
    let firstError: any = null;

    // Helper function to start a new upload with immediate callback
    const startUpload = (chunkInfo: { chunk: Blob; index: number }): Promise<void> => {
      // Don't start new uploads if there's already an error
      if (hasError) {
        return Promise.resolve();
      }

      activeCount++;
      this.logger.info(`Starting chunk [${chunkInfo.index}], active uploads: ${activeCount}`);

      return this.uploadChunkWithRetryAndCallback(chunkInfo, () => {
        this.updateLocalCache();
      })
        .then(() => {
          activeCount--;
          this.logger.info(`Chunk [${chunkInfo.index}] completed successfully, active uploads: ${activeCount}`);

          // Start next chunk immediately if available and no error occurred
          if (!hasError && chunkIndex < chunksToUpload.length) {
            startUpload(chunksToUpload[chunkIndex++]);
          }
        })
        .catch((error) => {
          activeCount--;
          this.logger.error(`Chunk [${chunkInfo.index}] failed, stopping all uploads:`, error);

          // Mark error state to stop new uploads
          hasError = true;
          if (!firstError) {
            firstError = error;
          }

          // Cancel all remaining uploads
          this.cancelToken.cancel();
        });
    };

    // Start initial uploads up to concurrency limit
    const initialPromises: Promise<void>[] = [];
    while (chunkIndex < chunksToUpload.length && initialPromises.length < concurrency) {
      initialPromises.push(startUpload(chunksToUpload[chunkIndex++]));
    }

    // Wait for all uploads to complete or fail
    await Promise.allSettled(initialPromises);

    // Wait for any remaining uploads started by callbacks to finish
    while (activeCount > 0) {
      await new Promise((resolve) => setTimeout(resolve, 100)); // Small delay to check active count
    }

    // Throw error immediately if any chunk failed
    if (firstError) {
      this.logger.error("Upload process stopped due to chunk failure");
      throw firstError;
    }

    this.logger.info("All chunks uploaded successfully.");
  }

  private async uploadChunkWithRetryAndCallback(
    chunkInfo: ChunkInfo,
    onComplete: () => void,
    retryCount = 0,
  ): Promise<void> {
    // Check for cancellation before attempting upload
    if (this.cancelToken.token.reason) {
      throw new Error("Upload cancelled by user");
    }

    try {
      if (chunkInfo.chunk instanceof EncryptedBlob && !this.task.policy.streaming_encryption) {
        chunkInfo.chunk = new Blob([await chunkInfo.chunk.bytes()]);
      }
      await this.uploadChunk(chunkInfo);
      this.logger.info(`Chunk [${chunkInfo.index}] uploaded successfully.`);
      this.busyInARow = 0;
      this.busyWait = 2000;
      onComplete(); // Call callback immediately after successful upload
    } catch (error) {
      // Don't retry if upload was cancelled
      if (this.cancelToken.token.reason) {
        throw error;
      }

      // Fylvo: in a connected storage an answer of the server is final, except "busy": the
      // same chunk is sent again after a wait (2 s doubling up to 60, at most 5 in a row).
      // "Outcome unknown" (40927) is never sent again.
      const code = (error as { response?: { code?: number } })?.response?.code;
      if (this.connected && code !== undefined) {
        if (code != ConnectedBusy || ++this.busyInARow >= 5) {
          throw error;
        }
        const wait = this.busyWait;
        this.busyWait = Math.min(this.busyWait * 2, 60000);
        await new Promise((resolve, reject) => {
          const timeout = setTimeout(resolve, wait);
          this.cancelToken.token.promise?.then(() => {
            clearTimeout(timeout);
            reject(new Error("Upload cancelled during retry delay"));
          });
        });
        return this.uploadChunkWithRetryAndCallback(chunkInfo, onComplete, retryCount);
      }

      if (retryCount < this.MAX_RETRIES) {
        this.logger.warn(
          `Chunk [${chunkInfo.index}] upload failed, retrying (${retryCount + 1}/${this.MAX_RETRIES}):`,
          error,
        );

        // Wait with exponential backoff, but check for cancellation
        const delay = Math.pow(2, retryCount) * 1000;
        await new Promise((resolve, reject) => {
          const timeout = setTimeout(resolve, delay);

          // Cancel the timeout if upload is cancelled
          this.cancelToken.token.promise?.then(() => {
            clearTimeout(timeout);
            reject(new Error("Upload cancelled during retry delay"));
          });
        });

        await this.uploadChunkWithRetryAndCallback(chunkInfo, onComplete, retryCount + 1);
      } else {
        this.logger.error(`Chunk [${chunkInfo.index}] upload failed after ${this.MAX_RETRIES} retries:`, error);
        throw error;
      }
    }
  }

  private getConcurrency(): number {
    return this.task.policy.chunk_concurrency || this.DEFAULT_CONCURRENCY;
  }

  private initBeforeUploadChunks() {
    this.chunks = utils.getChunks(this.task.blob, this.task.session?.chunk_size);
    const cachedInfo = utils.getResumeCtx(this.task, this.logger);
    if (cachedInfo == null) {
      this.task.chunkProgress = this.chunks.map(
        (_, index): ChunkProgress => ({
          loaded: 0,
          index,
        }),
      );
    }

    this.notifyResumeProgress();
  }

  protected abstract uploadChunk(chunkInfo: ChunkInfo): Promise<any>;

  protected updateChunkProgress(loaded: number, index: number, etag?: string) {
    // Serialize progress updates to avoid race conditions in concurrent uploads
    this.progressUpdateMutex = this.progressUpdateMutex.then(async () => {
      this.task.chunkProgress[index].loaded = loaded;
      if (etag) {
        this.task.chunkProgress[index].etag = etag;
      }
      this.notifyResumeProgress();
    });
  }

  private notifyResumeProgress() {
    this.progress = {
      total: this.getProgressInfoItem(utils.sumChunk(this.task.chunkProgress), this.task.file.size + 1),
      chunks: this.chunks.map((chunk, index) => {
        return this.getProgressInfoItem(this.task.chunkProgress[index].loaded, chunk.size, false);
      }),
    };
    this.subscriber.onProgress(this.progress);
  }

  private updateLocalCache() {
    utils.setResumeCtx(this.task, this.logger);
  }
}
