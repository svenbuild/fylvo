import { fileExtension } from "./index.ts";

// Fylvo: a blank document for a new or empty file opened in a Craft editor, as the editors
// cannot save a document they made themselves in the right format. undefined when there is none
// for the kind of file, or it did not arrive as a document.
export const loadBlankTemplate = async (name: string): Promise<ArrayBuffer | undefined> => {
  const ext = fileExtension(name) ?? "";
  if (!name.includes(".") || !["docx", "xlsx", "pptx"].includes(ext)) {
    return undefined;
  }
  try {
    const res = await fetch(`/static/craft-blank/blank.${ext}`);
    const bytes = res.ok ? await res.arrayBuffer() : undefined;
    // A document is a zip package ("PK"), not the web app's page.
    const head = bytes ? new Uint8Array(bytes, 0, Math.min(2, bytes.byteLength)) : undefined;
    return head && head[0] == 0x50 && head[1] == 0x4b ? bytes : undefined;
  } catch (e) {
    return undefined;
  }
};
