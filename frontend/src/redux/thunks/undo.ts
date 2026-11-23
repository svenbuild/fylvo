import { FileResponse } from "../../api/explorer.ts";

// Fylvo: the last own action in the file manager, for Ctrl+Z. Only one step is kept. Actions
// that cannot be undone cleanly (copies, final deletes, anything in a connected storage except a
// rename) are kept as "none", so Ctrl+Z never reaches back past them to an older action.
export type UndoEntry =
  // file is the renamed file as the server returned it.
  | { kind: "rename"; file: FileResponse; oldName: string }
  // files are the moved files at their new place, each with the folder it came from.
  | { kind: "move"; files: { file: FileResponse; from: string }[] }
  // files are the files as they were before they went to the trash, since the time (in ms) the
  // delete started.
  | { kind: "trash"; files: FileResponse[]; since: number }
  | { kind: "none" };

let last: UndoEntry | undefined;

export function rememberUndo(entry: UndoEntry) {
  last = entry;
}

export function takeUndo(): UndoEntry | undefined {
  const entry = last;
  last = undefined;
  return entry;
}
