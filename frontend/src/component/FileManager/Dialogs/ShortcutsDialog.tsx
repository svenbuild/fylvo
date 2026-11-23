import { DialogContent, Table, TableBody, TableCell, TableRow, Typography } from "@mui/material";
import { useTranslation } from "react-i18next";
import { setShortcutsDialog } from "../../../redux/globalStateSlice.ts";
import { useAppDispatch, useAppSelector } from "../../../redux/hooks.ts";
import { isMacbook } from "../../../redux/thunks/file.ts";
import { modKey } from "../../../redux/thunks/shortcuts.ts";
import DraggableDialog from "../../Dialogs/DraggableDialog.tsx";
import { KeyIndicator } from "../../Frame/NavBar/SearchBar.tsx";

// Fylvo: the keyboard shortcuts of the file view. Each row: the keys (alternatives separated by
// "/", combinations by "+") and what they do.
const shortcuts = (del: string): [string[][], string][] => [
  [[[modKey(), "C"]], "keyCopy"],
  [[[modKey(), "X"]], "keyCut"],
  [[[modKey(), "V"]], "keyPaste"],
  [[[modKey(), "Z"]], "keyUndo"],
  [[[modKey(), "A"]], "keySelectAll"],
  [isMacbook ? [[del], ["⌘", "⌫"]] : [[del]], "keyDelete"],
  [[["F2"]], "keyRename"],
  [[["Enter"]], "keyOpen"],
  [[["⌫"], [isMacbook ? "⌥" : "Alt", "↑"]], "keyUp"],
  [[["Esc"]], "keyEscape"],
  [[["/"]], "keySearch"],
];

const ShortcutsDialog = () => {
  const { t } = useTranslation();
  const dispatch = useAppDispatch();
  const open = useAppSelector((state) => state.globalState.shortcutsDialogOpen);

  return (
    <DraggableDialog
      title={t("application:shortcuts.title")}
      dialogProps={{
        open: !!open,
        onClose: () => dispatch(setShortcutsDialog(false)),
        fullWidth: true,
        maxWidth: "xs",
      }}
    >
      <DialogContent sx={{ pt: 0 }}>
        <Table size="small">
          <TableBody>
            {shortcuts(t("application:shortcuts.delKey")).map(([keys, label]) => (
              <TableRow key={label}>
                <TableCell sx={{ whiteSpace: "nowrap", pl: 0 }}>
                  {keys.map((combo, i) => (
                    <span key={i}>
                      {i > 0 && " / "}
                      {combo.map((k, j) => (
                        <span key={k}>
                          {j > 0 && "+"}
                          <KeyIndicator>{k}</KeyIndicator>
                        </span>
                      ))}
                    </span>
                  ))}
                </TableCell>
                <TableCell sx={{ pr: 0 }}>{t(`application:shortcuts.${label}`)}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
        <Typography variant="body2" color="text.secondary" sx={{ mt: 1.5 }}>
          {t("application:shortcuts.where")}
        </Typography>
      </DialogContent>
    </DraggableDialog>
  );
};

export default ShortcutsDialog;
