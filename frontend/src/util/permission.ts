import { FileResponse, NavigatorCapability } from "../api/explorer.ts";
import Boolset from "./boolset.ts";
import CrUri, { Filesystem } from "./uri.ts";

// Fylvo: the one place that decides whether the current user may change a file or create files
// inside a folder. Outside shares ownership decides. Inside a share the capability set sent by the
// backend decides: only members with the edit right get the write bits, link shares and readers never.
export function canModifyFile(file: FileResponse): boolean {
  if (new CrUri(file.path).fs() == Filesystem.share) {
    return new Boolset(file.capability).enabled(NavigatorCapability.upload_file);
  }
  return !!file.owned;
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
