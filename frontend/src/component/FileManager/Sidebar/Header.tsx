import { Box, IconButton, Skeleton, Typography } from "@mui/material";
import { FileResponse } from "../../../api/explorer.ts";
import { closeShareReadme, closeSidebar } from "../../../redux/globalStateSlice.ts";
import { useAppDispatch } from "../../../redux/hooks.ts";
import Dismiss from "../../Icons/Dismiss.tsx";
import FileIcon from "../Explorer/FileIcon.tsx";

export interface HeaderProps {
  target: FileResponse | undefined | null;
  variant?: "readme";
}
const Header = ({ target, variant }: HeaderProps) => {
  const dispatch = useAppDispatch();
  return (
    <Box
      sx={{
        display: "flex",
        alignItems: "center",
        px: 2,
        py: "4px",
        gap: 1,
        minHeight: 36,
        flexShrink: 0,
        borderBottom: 1,
        borderColor: "divider",
        "@media (max-width: 599.95px), (pointer: coarse)": { minHeight: 40 },
      }}
    >
      {target !== null && <FileIcon sx={{ p: 0, display: "flex" }} loading={target == undefined} file={target} type={target?.type} />}
      {target !== null && (
        <Box sx={{ flexGrow: 1, minWidth: 0 }}>
          <Typography color="textPrimary" sx={{ overflowWrap: "anywhere" }} variant={"subtitle2"}>
            {target && target.name}
            {!target && <Skeleton variant={"text"} width={75} />}
          </Typography>
        </Box>
      )}
      <IconButton
        onClick={() => {
          dispatch(variant == "readme" ? closeShareReadme() : closeSidebar());
        }}
        sx={{
          ml: "auto",
          alignSelf: "start",
          "&.Mui-focusVisible": { outline: "1px solid", outlineColor: "primary.main", outlineOffset: -1 },
          "@media (max-width: 599.95px), (pointer: coarse)": { minWidth: 40, minHeight: 40 },
        }}
        size={"small"}
      >
        <Dismiss fontSize={"small"} />
      </IconButton>
    </Box>
  );
};

export default Header;
