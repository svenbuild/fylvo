import { Box, List, ListItem, ListItemAvatar, ListItemButton, ListItemText } from "@mui/material";
import { Trans, useTranslation } from "react-i18next";
import React, { useCallback } from "react";
// @ts-ignore
import Highlighter from "react-highlight-words";
import { useAppDispatch } from "../../../redux/hooks.ts";
import { SearchOutlined } from "@mui/icons-material";
import { FileType } from "../../../api/explorer.ts";
import FileBadge from "../FileBadge.tsx";
import { quickSearch } from "../../../redux/thunks/filemanager.ts";
import { FileManagerIndex } from "../FileManager.tsx";

export interface FullSearchOptionProps {
  options: string[];
  keyword: string;
}

const FullSearchOption = ({ options, keyword }: FullSearchOptionProps) => {
  const { t } = useTranslation();
  const dispatch = useAppDispatch();

  const onClick = useCallback(
    (base: string) => () => dispatch(quickSearch(FileManagerIndex.main, base, keyword)),
    [keyword, dispatch],
  );

  return (
    <List sx={{ width: "100%", p: "4px" }} dense>
      {options.map((option) => (
        <ListItem disablePadding dense>
          <ListItemButton
            onClick={onClick(option)}
            sx={(theme) => ({
              p: "0 8px",
              gap: "4px",
              minHeight: 32,
              [theme.breakpoints.down("sm")]: { minHeight: 40 },
              "@media (pointer: coarse)": { minHeight: 40 },
            })}
          >
            <ListItemAvatar sx={{ minWidth: 24, display: "flex", color: "text.secondary" }}>
              <SearchOutlined fontSize="small" />
            </ListItemAvatar>
            <ListItemText
              primary={
                <Trans
                  ns={"application"}
                  i18nKey={"navbar.searchIn"}
                  values={{
                    keywords: keyword,
                  }}
                  components={[<Box component={"span"} sx={{ fontWeight: 600 }} />]}
                />
              }
              slotProps={{
                primary: {
                  variant: "body2",
                  sx: { overflowWrap: "anywhere" },
                },
              }}
            />
            <FileBadge
              clickable
              variant={"outlined"}
              sx={{ px: 1, my: "4px", minWidth: 0, maxWidth: { xs: 140, sm: 160 } }}
              simplifiedFile={{
                path: option,
                type: FileType.folder,
              }}
            />
          </ListItemButton>
        </ListItem>
      ))}
    </List>
  );
};

export default FullSearchOption;
