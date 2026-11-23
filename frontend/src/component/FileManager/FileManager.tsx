import { Box, Stack, useMediaQuery, useTheme } from "@mui/material";
import React, { useCallback, useEffect, useRef } from "react";
import useNavigation from "../../hooks/useNavigation.tsx";
import { clearSelected } from "../../redux/fileManagerSlice.ts";
import { resetDialogs } from "../../redux/globalStateSlice.ts";
import { useAppDispatch, useAppSelector } from "../../redux/hooks.ts";
import { isMacbook } from "../../redux/thunks/file.ts";
import { resetFm, selectAll } from "../../redux/thunks/filemanager.ts";
import {
  cancelCut,
  clipSelection,
  deleteSelected,
  goUp,
  openSelected,
  pasteClipboard,
  renameHint,
  undoLast,
} from "../../redux/thunks/shortcuts.ts";
import ImageViewer from "../Viewers/ImageViewer/ImageViewer.tsx";
import Explorer from "./Explorer/Explorer.tsx";
import { FmIndexContext } from "./FmIndexContext.tsx";
import PaginationFooter from "./Pagination/PaginationFooter.tsx";
import { ReadMe } from "./ReadMe/ReadMe.tsx";
import Sidebar from "./Sidebar/Sidebar.tsx";
import SidebarDialog from "./Sidebar/SidebarDialog.tsx";
import NavHeader from "./TopBar/NavHeader.tsx";

export const FileManagerIndex = {
  main: 0,
  selector: 1,
};

export interface FileManagerProps {
  index?: number;
  initialPath?: string;
  skipRender?: boolean;
}

export const FileManager = ({ index = 0, initialPath, skipRender }: FileManagerProps) => {
  const dispatch = useAppDispatch();
  const theme = useTheme();
  const isMobile = useMediaQuery(theme.breakpoints.down("sm"));
  const isTablet = useMediaQuery(theme.breakpoints.down("md"));

  useNavigation(index, initialPath);

  useEffect(() => {
    if (index == FileManagerIndex.main) {
      dispatch(resetDialogs());
      return () => {
        dispatch(resetFm(index));
      };
    }
  }, []);

  // Fylvo: when a folder opens, the focused file card goes away and the focus with it. The file
  // view takes it back, so the keys keep working (not when the focus is somewhere else).
  const viewRef = useRef<HTMLDivElement>(null);
  const list = useAppSelector((state) => state.fileManager[index].list);
  useEffect(() => {
    if (index == FileManagerIndex.main && (!document.activeElement || document.activeElement == document.body)) {
      viewRef.current?.focus({ preventScroll: true });
    }
  }, [list]);

  // Fylvo: the keys of a file manager, only while the file view has the focus. Keys typed into
  // a field, or in a menu or dialog opened from here (they are portals), are left alone.
  const onKeyDown = useCallback(
    (e: React.KeyboardEvent<HTMLElement>) => {
      const target = e.target as HTMLElement;
      if (
        index != FileManagerIndex.main ||
        e.nativeEvent.isComposing ||
        !e.currentTarget.contains(target) ||
        target.isContentEditable ||
        target.closest("input, textarea, select")
      ) {
        return;
      }
      const mod = isMacbook ? e.metaKey : e.ctrlKey;
      const otherMod = isMacbook ? e.ctrlKey : e.metaKey;
      const textSelected = !!window.getSelection()?.toString();
      const key = e.key.length == 1 ? e.key.toLowerCase() : e.key;
      let action: (() => unknown) | undefined;
      if (mod && !otherMod && !e.altKey && !e.shiftKey) {
        switch (key) {
          case "a":
            action = () => dispatch(selectAll(index));
            break;
          case "c":
          case "x":
            // Marked text on the page is copied as text.
            if (!textSelected) {
              action = () => dispatch(clipSelection(index, key == "x"));
            }
            break;
          case "v":
            action = () => dispatch(pasteClipboard(index));
            break;
          case "z":
            action = () => dispatch(undoLast(index));
            break;
          case "Backspace":
            if (isMacbook) {
              action = () => dispatch(deleteSelected(index));
            }
            break;
        }
      } else if (!e.ctrlKey && !e.metaKey && !e.shiftKey) {
        switch (key) {
          case "Delete":
            action = !e.altKey ? () => dispatch(deleteSelected(index)) : undefined;
            break;
          case "F2":
            action = !e.altKey ? () => dispatch(renameHint(index)) : undefined;
            break;
          case "Enter":
            // A focused button (breadcrumb, toolbar) keeps its own Enter; a file card does not.
            if (!e.altKey && (!target.closest("button, a, [role='button']") || target.closest("[data-rect-id]"))) {
              action = () => dispatch(openSelected(index));
            }
            break;
          case "Backspace":
            action = !e.altKey ? () => dispatch(goUp(index)) : undefined;
            break;
          case "ArrowUp":
            action = e.altKey ? () => dispatch(goUp(index)) : undefined;
            break;
          case "Escape":
            action = !e.altKey
              ? () => {
                  dispatch(clearSelected({ index, value: {} }));
                  dispatch(cancelCut());
                }
              : undefined;
            break;
        }
      }
      if (action) {
        e.preventDefault();
        // After the action (for example a question that closed and took the focus with the
        // deleted card), the file view takes the focus back if nothing else has it.
        const view = e.currentTarget;
        Promise.resolve(action()).finally(() =>
          setTimeout(() => {
            if (!document.activeElement || document.activeElement == document.body) {
              view.focus({ preventScroll: true });
            }
          }, 300),
        );
      }
    },
    [dispatch, index],
  );

  if (skipRender) {
    return null;
  }

  return (
    <FmIndexContext.Provider value={index}>
      <Stack
        onClick={(e) => {
          e.currentTarget.focus();
        }}
        ref={viewRef}
        onKeyDown={onKeyDown}
        data-fm-index={index}
        direction={"column"}
        sx={{
          flexGrow: 1,
          minWidth: 0,
          minHeight: 0,
          mb: index == FileManagerIndex.main && !isMobile ? 1 : 0,
          overflow: "auto",
          "&:focus": {
            outline: "none",
          },
        }}
        tabIndex={0}
        spacing={0}
      >
        <NavHeader />
        <Box sx={{ display: "flex", flexGrow: 1, minHeight: 0, pt: { xs: 0, sm: 1 }, overflowY: "auto" }}>
          <Explorer />
          {index == FileManagerIndex.main && (isTablet ? <SidebarDialog /> : <Sidebar />)}
          {index == FileManagerIndex.main && <ReadMe />}
        </Box>
        <PaginationFooter />
      </Stack>
      {index == FileManagerIndex.main && <ImageViewer />}
    </FmIndexContext.Provider>
  );
};
