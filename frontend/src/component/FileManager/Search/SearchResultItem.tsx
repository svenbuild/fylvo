import { ListItem, ListItemAvatar, ListItemButton, ListItemText } from "@mui/material";
import React from "react";
import { FileResponse, FileType } from "../../../api/explorer.ts";
import { raisedSelectedSurface } from "../../../fylvoTheme.ts";
import { ContextMenuTypes } from "../../../redux/fileManagerSlice.ts";
import { useAppDispatch } from "../../../redux/hooks.ts";
import { openFileContextMenu } from "../../../redux/thunks/file.ts";
import CrUri from "../../../util/uri.ts";
import FileIcon from "../Explorer/FileIcon.tsx";
import FileBadge from "../FileBadge.tsx";
import { FileManagerIndex } from "../FileManager.tsx";

export interface SearchResultItemProps {
  file: FileResponse;
  primary: React.ReactNode;
  secondary: React.ReactNode;
}

const SearchResultItem = ({ file, primary, secondary }: SearchResultItemProps) => {
  const dispatch = useAppDispatch();

  return (
    <ListItem disablePadding dense>
      <ListItemButton
        sx={(theme) => ({
          p: "0 8px",
          gap: "4px",
          minHeight: 34,
          [theme.breakpoints.down("sm")]: { minHeight: 40 },
          "@media (pointer: coarse)": { minHeight: 40 },
          "& .highlight-marker, & em": {
            color: theme.palette.text.primary,
            backgroundColor: raisedSelectedSurface(theme),
            boxShadow: "none",
            borderRadius: 3,
            fontStyle: "normal",
            fontWeight: 600,
          },
        })}
        onClick={(e) =>
          dispatch(openFileContextMenu(FileManagerIndex.main, file, true, e, ContextMenuTypes.searchResult))
        }
      >
        <ListItemAvatar sx={{ minWidth: 24, display: "flex" }}>
          <FileIcon
            variant={"default"}
            file={file}
            sx={{ p: 0, display: "flex" }}
            iconProps={{
              sx: {
                fontSize: 18,
                height: 18,
                width: 18,
              },
            }}
          />
        </ListItemAvatar>
        <ListItemText
          sx={{ my: "2px", overflowWrap: "anywhere" }}
          primary={primary}
          secondary={secondary}
          slotProps={{
            primary: {
              variant: "body2",
            },
            secondary: {
              variant: "caption",
              component: "div",
            },
          }}
        />
        <FileBadge
          clickable
          variant={"outlined"}
          sx={{ px: 1, minWidth: 0, maxWidth: { xs: 140, sm: 160 } }}
          simplifiedFile={{
            path: new CrUri(file.path).parent().toString(),
            type: FileType.folder,
          }}
        />
      </ListItemButton>
    </ListItem>
  );
};

export default SearchResultItem;
