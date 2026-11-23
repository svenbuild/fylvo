import { inConnectedStorage } from "../../../api/connected.ts";
import { FileResponse, Metadata, Viewer, ViewerType } from "../../../api/explorer.ts";

// Id of the built-in archive viewer (builtInViewers.archive in redux/thunks/viewer.ts, not
// imported here because that module imports the action rules, which use this one).
const archiveViewer = "archive";

// Fylvo: in a connected storage only viewers that read the file through its address work, and
// the Office editor of the server (it opens its own session for the file); the archive viewer
// needs a file of this server.
// The Office editor needs the file itself: not a shortcut, a document of the provider or a row
// that cannot be used.
export const usableViewers = (file: FileResponse, viewers?: Viewer[]) => {
  if (!inConnectedStorage(file.path)) {
    return viewers ?? [];
  }
  const noOffice = [Metadata.connected_link, Metadata.connected_export, Metadata.connected_unsupported].some(
    (key) => file.metadata?.[key] !== undefined,
  );
  return (viewers ?? []).filter((v) => v.id != archiveViewer && !(noOffice && v.type == ViewerType.wopi));
};
