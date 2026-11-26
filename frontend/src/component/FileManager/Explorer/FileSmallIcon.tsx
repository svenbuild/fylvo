import { Box, Fade } from "@mui/material";
import { memo, useCallback, useContext } from "react";
import { TransitionGroup } from "react-transition-group";
import { FileResponse } from "../../../api/explorer.ts";
import { useAppDispatch, useAppSelector } from "../../../redux/hooks.ts";
import { fileIconClicked } from "../../../redux/thunks/file.ts";
import CheckmarkCircle from "../../Icons/CheckmarkCircle.tsx";
import CheckUnchecked from "../../Icons/CheckUnchecked.tsx";
import FileIcon from "./FileIcon.tsx";

import { FmIndexContext } from "../FmIndexContext.tsx";

export interface FileSmallIconProps {
  selected: boolean;
  file: FileResponse;
  loading?: boolean;
  ignoreHovered?: boolean;
  variant?: "list" | "grid";
}

const FileSmallIcon = memo(({ selected, variant, loading, file, ignoreHovered }: FileSmallIconProps) => {
  const dispatch = useAppDispatch();
  const fmIndex = useContext(FmIndexContext);
  const hovered = useAppSelector((state) => state.fileManager[fmIndex].multiSelectHovered[file.path]);
  const onIconClick = useCallback(
    (e: React.MouseEvent<HTMLElement>) => {
      if (!loading) {
        return dispatch(fileIconClicked(fmIndex, file, e));
      }
    },
    [file, loading, dispatch],
  );
  const iconSize = variant === "list" ? 16 : 18;
  const showCheckbox = selected || (hovered && !ignoreHovered);
  // Fylvo: no fade when the file appears (rows come and go while scrolling); changes still fade.
  return (
    <TransitionGroup
      appear={false}
      onClick={onIconClick}
      style={{ position: "relative", display: "grid", placeItems: "center", flexShrink: 0, width: 24, height: 24 }}
    >
      {!showCheckbox && (
        <Fade>
          <FileIcon
            file={file}
            loading={loading}
            variant="small"
            iconProps={{ sx: { width: iconSize, height: iconSize, fontSize: iconSize } }}
            sx={{ position: "absolute", display: "flex", p: 0 }}
          />
        </Fade>
      )}
      {showCheckbox && (
        <Fade>
          <Box sx={{ position: "absolute", display: "flex" }}>
            {selected ? (
              <CheckmarkCircle color="primary" fontSize="small" />
            ) : (
              <CheckUnchecked sx={{ color: "text.secondary" }} fontSize="small" />
            )}
          </Box>
        </Fade>
      )}
    </TransitionGroup>
  );
});

export default FileSmallIcon;
