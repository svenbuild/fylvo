import { inConnectedStorage } from "../../../api/connected.ts";
import { FileResponse, Viewer, ViewerType } from "../../../api/explorer.ts";

// Id of the built-in archive viewer (builtInViewers.archive in redux/thunks/viewer.ts, not
// imported here because that module imports the action rules, which use this one).
const archiveViewer = "archive";

// Fylvo: in a connected storage only viewers that read the file through its address work;
// Office (WOPI sessions) and the archive viewer need a file of this server.
export const usableViewers = (file: FileResponse, viewers?: Viewer[]) =>
  inConnectedStorage(file.path)
    ? (viewers ?? []).filter((v) => v.type != ViewerType.wopi && v.id != archiveViewer)
    : viewers ?? [];
