import { inConnectedStorage } from "../../../api/connected.ts";
import { FileResponse, Viewer } from "../../../api/explorer.ts";

// Id of the built-in archive viewer (builtInViewers.archive in redux/thunks/viewer.ts, not
// imported here because that module imports the action rules, which use this one).
const archiveViewer = "archive";

// Fylvo: in a connected storage only viewers that read the file through its address work, and
// the Office editor of the server (it opens its own session for the file); the archive viewer
// needs a file of this server.
export const usableViewers = (file: FileResponse, viewers?: Viewer[]) =>
  inConnectedStorage(file.path) ? (viewers ?? []).filter((v) => v.id != archiveViewer) : viewers ?? [];
