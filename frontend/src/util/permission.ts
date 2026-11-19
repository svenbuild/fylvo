import { FileResponse, NavigatorCapability } from "../api/explorer.ts";
import Boolset from "./boolset.ts";
import CrUri, { Filesystem } from "./uri.ts";

// Fylvo: the two places that decide what the current user may do with files. Outside shares ownership
// decides. Inside a share the capability set sent by the backend decides:
// - upload_file: add new files and folders (editors of member shares and visitors of upload links).
// - rename_file: change existing files (only editors of member shares). Upload visitors never get it.
function shareCapability(file: FileResponse, capability: number): boolean | undefined {
  if (new CrUri(file.path).fs() == Filesystem.share) {
    return new Boolset(file.capability).enabled(capability);
  }
  return undefined;
}

// canAddTo: may create folders and upload files and folders into this folder.
export function canAddTo(folder: FileResponse): boolean {
  return shareCapability(folder, NavigatorCapability.upload_file) ?? !!folder.owned;
}

// canModifyFile: may rename, delete, move, copy, or edit the content of this file.
export function canModifyFile(file: FileResponse): boolean {
  return shareCapability(file, NavigatorCapability.rename_file) ?? !!file.owned;
}

// isUploadOnly: folder of an "upload only" link. Visitors may add files but never see or download its content.
export function isUploadOnly(folder?: FileResponse): boolean {
  return (
    !!folder &&
    shareCapability(folder, NavigatorCapability.upload_file) === true &&
    !new Boolset(folder.capability).enabled(NavigatorCapability.download_file)
  );
}

// canCopyMoveTo checks if the files can be copied or moved to the destination.
export function canCopyMoveTo(files: FileResponse[], dst: string, isCopy: boolean): boolean {
  const dstUri = new CrUri(dst);
  const srcUri = new CrUri(files[0].path);
  // Fylvo: inside a member share with edit right, copy and move stay within the same share.
  if (srcUri.fs() == Filesystem.share) {
    return dstUri.fs() == Filesystem.share && dstUri.id() == srcUri.id() && files.every(canModifyFile);
  }
  if (isCopy) {
    return srcUri.fs() == dstUri.fs() && srcUri.fs() == Filesystem.my;
  } else {
    switch (srcUri.fs()) {
      case Filesystem.my:
        return dstUri.fs() == Filesystem.my || dstUri.fs() == Filesystem.trash;
      case Filesystem.trash:
        return dstUri.fs() == Filesystem.my;
    }
  }

  return false;
}
