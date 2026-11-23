import { FileResponse } from "../../api/explorer.ts";
import SessionManager from "../../session";

// Fylvo: the last own action in the file manager, for Ctrl+Z. Only one step is kept. Actions
// that cannot be undone cleanly (copies, final deletes, failed actions, anything in a connected
// storage) are kept as "none", so Ctrl+Z never reaches back past them to an older action.
export type UndoEntry =
  // file is the renamed file as the server returned it.
  | { kind: "rename"; file: FileResponse; oldName: string }
  // files are the moved files at their new place, each with the folder it came from.
  | { kind: "move"; files: { file: FileResponse; from: string }[] }
  // files are the files as they were before they went to the trash.
  | { kind: "trash"; files: FileResponse[] }
  | { kind: "none" };

let last: { entry: UndoEntry; user?: string } | undefined;
// While an undo runs, the actions it uses do not record themselves.
let undoing = 0;

const currentUser = () => SessionManager.currentLoginOrNull()?.user.id;

export function rememberUndo(entry: UndoEntry) {
  if (undoing == 0) {
    last = { entry, user: currentUser() };
  }
}

// The last entry, only for the account that made it. It is taken, so it is undone only once.
export function takeUndo(): UndoEntry | undefined {
  const taken = last;
  last = undefined;
  return taken && taken.user == currentUser() ? taken.entry : undefined;
}

export async function withoutRecording(run: () => Promise<unknown>) {
  undoing++;
  try {
    await run();
  } finally {
    undoing--;
  }
}
