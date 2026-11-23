import { Box, ButtonBase, IconButton, Tooltip, Typography } from "@mui/material";
import { useTranslation } from "react-i18next";
import { FileResponse, FileType } from "../../../api/explorer.ts";
import { hoverSurface } from "../../../fylvoTheme.ts";
import { useAppDispatch } from "../../../redux/hooks.ts";
import { navigateToPath } from "../../../redux/thunks/filemanager.ts";
import { sizeToString } from "../../../util";
import CrUri from "../../../util/uri.ts";
import TimeBadge from "../../Common/TimeBadge.tsx";
import FileIcon from "../../FileManager/Explorer/FileIcon.tsx";
import { FileManagerIndex } from "../../FileManager/FileManager.tsx";
import StarFilled from "../../Icons/StarFilled.tsx";

// Fylvo: a calm list of files for the start page and the favorites page. A folder opens; a file
// opens its folder.

export const openFileLocation = (file: FileResponse) => {
  if (file.type == FileType.folder) {
    return navigateToPath(FileManagerIndex.main, file.path);
  }
  return navigateToPath(FileManagerIndex.main, new CrUri(file.path).parent().toString());
};

const folderOf = (file: FileResponse) => {
  try {
    const elements = new CrUri(file.path).parent().elements();
    return elements.length ? elements.join(" / ") : undefined;
  } catch (e) {
    return undefined;
  }
};

export interface FileRowsProps {
  files: FileResponse[];
  onUnstar?: (file: FileResponse) => void;
}

const FileRows = ({ files, onUnstar }: FileRowsProps) => {
  const { t } = useTranslation("application");
  const dispatch = useAppDispatch();
  return (
    <Box sx={{ border: 1, borderColor: "divider", borderRadius: 1, overflow: "hidden" }}>
      {files.map((file, i) => (
        <Box
          key={file.id + file.path}
          sx={{
            display: "flex",
            alignItems: "center",
            borderTop: i == 0 ? "none" : 1,
            borderColor: "divider",
            "&:hover": { backgroundColor: (theme) => hoverSurface(theme) },
          }}
        >
          <ButtonBase
            onClick={() => dispatch(openFileLocation(file))}
            sx={{
              flexGrow: 1,
              minWidth: 0,
              justifyContent: "flex-start",
              gap: 1,
              px: 1.5,
              py: 0.75,
              textAlign: "left",
              "@media (pointer: coarse)": { py: 1.25 },
            }}
          >
            <FileIcon variant="small" file={file} sx={{ p: 0, display: "flex" }} iconProps={{ fontSize: "small" }} />
            <Box sx={{ minWidth: 0, flexGrow: 1 }}>
              <Typography variant="body2" noWrap>
                {file.name}
              </Typography>
              <Typography variant="caption" color="text.secondary" noWrap component="div">
                {[folderOf(file), file.type == FileType.file ? sizeToString(file.size) : undefined]
                  .filter(Boolean)
                  .join(" · ")}
                {file.type == FileType.file && (
                  <>
                    {" · "}
                    <TimeBadge variant="inherit" datetime={file.updated_at} />
                  </>
                )}
              </Typography>
            </Box>
          </ButtonBase>
          {onUnstar && (
            <Tooltip title={t("customize.unstar")}>
              <IconButton size="small" sx={{ mr: 1 }} onClick={() => onUnstar(file)}>
                <StarFilled fontSize="small" />
              </IconButton>
            </Tooltip>
          )}
        </Box>
      ))}
    </Box>
  );
};

export default FileRows;
