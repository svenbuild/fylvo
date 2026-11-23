import { inConnectedStorage } from "../../../api/connected.ts";
import { FileResponse, FileType, Metadata, Viewer, ViewerType } from "../../../api/explorer.ts";
import { fileExtension } from "../../../util";

// Id of the built-in archive viewer (builtInViewers.archive in redux/thunks/viewer.ts, not
// imported here because that module imports the action rules, which use this one).
const archiveViewer = "archive";

// Fylvo: in a connected storage only viewers that read the file through its address work, and
// the Office editor of the server (it opens its own session for the file); the archive viewer
// needs a file of this server.
// The type an Office viewer has to handle: for a file its extension ("" for a name without a
// dot, as the server sees it), undefined for a folder or the place of a new document from a
// template, which take any Office viewer ("show all" lists every viewer).
const officeType = (file: FileResponse) => {
  if (file.type != FileType.file) {
    return undefined;
  }
  return file.name.includes(".") ? fileExtension(file.name) ?? "" : "";
};

// The Office editor needs the file itself: not a shortcut, a document of the provider or a row
// that cannot be used.
export const usableViewers = (file: FileResponse, viewers?: Viewer[]) => {
  if (!inConnectedStorage(file.path)) {
    return viewers ?? [];
  }
  const noOffice = [
    Metadata.connected_link,
    Metadata.connected_export,
    Metadata.connected_external_url,
    Metadata.connected_unsupported,
  ].some((key) => file.metadata?.[key] !== undefined);
  const ext = officeType(file);
  return (viewers ?? []).filter(
    (v) =>
      v.id != archiveViewer &&
      !(v.type == ViewerType.wopi && (noOffice || (ext !== undefined && !v.exts.includes(ext)))),
  );
};
