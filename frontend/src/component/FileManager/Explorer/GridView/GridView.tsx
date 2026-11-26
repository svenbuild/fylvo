import { Box, Typography, useMediaQuery, useTheme } from "@mui/material";
import React, { useCallback, useContext, useLayoutEffect, useMemo, useRef, useState } from "react";
import { Virtuoso } from "react-virtuoso";
import { useTranslation } from "react-i18next";
import { FileResponse, FileType } from "../../../../api/explorer.ts";
import { useAppSelector } from "../../../../redux/hooks.ts";
import DndWrappedFile from "../../Dnd/DndWrappedFile.tsx";
import { FmIndexContext } from "../../FmIndexContext.tsx";
import GridFile from "./GridFile.tsx";
import { CrUriPrefix } from "../../../../util/uri.ts";

export interface GridViewProps {
  [key: string]: any;
}

export interface FmFile extends FileResponse {
  id: string;
  first?: boolean;
  placeholder?: boolean;
}

interface listComponents {
  Folders?: JSX.Element[];
  Files: JSX.Element[];
}

const gridGap = 8;

// Fylvo: the grid shows only the rows near the visible part (a folder can hold thousands of files).
// The rows are cut to as many columns as `repeat(auto-fill, minmax(<min>, 1fr))` would make.
function columnCount(width: number, minWidth: number) {
  return Math.max(1, Math.floor((width + gridGap) / (minWidth + gridGap)));
}

type GridRow = { title: string; first: boolean } | { cells: JSX.Element[]; last: boolean };

function addSection(rows: GridRow[], title: string | undefined, cells: JSX.Element[], columns: number) {
  if (title !== undefined) {
    rows.push({ title, first: rows.length == 0 });
  }
  for (let i = 0; i < cells.length; i += columns) {
    rows.push({ cells: cells.slice(i, i + columns), last: i + columns >= cells.length });
  }
}

function scrollParentOf(element: HTMLElement | null) {
  for (let parent = element?.parentElement; parent; parent = parent.parentElement) {
    if (/auto|scroll/.test(getComputedStyle(parent).overflowY)) {
      return parent;
    }
  }
  return undefined;
}

export const loadingPlaceHolderNumb = 3;

const GridView = React.forwardRef(({ ...rest }: GridViewProps, ref) => {
  const { t } = useTranslation("application");
  const fmIndex = useContext(FmIndexContext);
  const files = useAppSelector((state) => state.fileManager[fmIndex].list?.files);
  const mixedType = useAppSelector((state) => state.fileManager[fmIndex].list?.mixed_type);
  const pagination = useAppSelector((state) => state.fileManager[fmIndex].list?.pagination);
  const showThumb = useAppSelector((state) => state.fileManager[fmIndex].showThumb);
  const search_params = useAppSelector((state) => state.fileManager[fmIndex]?.search_params);
  const list = useMemo(() => {
    const list: listComponents = {
      Files: [],
    };
    if (files) {
      files.forEach((file, index) => {
        if (file.type === FileType.folder && !mixedType) {
          if (!list.Folders) {
            list.Folders = [];
          }
          list.Folders.push(
            <Box key={file.id} sx={{ minWidth: 0 }}>
              <DndWrappedFile
                component={GridFile}
                search={search_params}
                index={index}
                showThumb={mixedType}
                file={file}
              />
            </Box>,
          );
        } else {
          list.Files.push(
            <Box key={file.id} sx={{ minWidth: 0 }}>
              <DndWrappedFile
                component={GridFile}
                search={search_params}
                index={index}
                showThumb={showThumb}
                file={file}
              />
            </Box>,
          );
        }
      });

      // Add loading placeholder if there is next page
      if (pagination && pagination.next_token) {
        for (let i = 0; i < loadingPlaceHolderNumb; i++) {
          const id = `loadingPlaceholder-${pagination.next_token}-${i}`;
          const loadingPlaceholder = (
            <Box key={id} sx={{ minWidth: 0 }}>
              <DndWrappedFile
                component={GridFile}
                showThumb={list.Files.length > 0 ? showThumb : mixedType}
                file={{
                  ...files[0],
                  path: `${CrUriPrefix}${id}`,
                  id: id,
                  first: i == 0,
                  placeholder: true,
                }}
              />
            </Box>
          );
          const _ =
            list.Files.length > 0 ? list.Files.push(loadingPlaceholder) : list.Folders?.push(loadingPlaceholder);
        }
      }
    }
    return list;
  }, [files, mixedType, pagination, showThumb]);
  const theme = useTheme();
  const isMdUp = useMediaQuery(theme.breakpoints.up("md"), { noSsr: true });
  const boxRef = useRef<HTMLElement | null>(null);
  const [scrollParent, setScrollParent] = useState<HTMLElement>();
  const [width, setWidth] = useState(0);
  useLayoutEffect(() => {
    const box = boxRef.current;
    if (!box) {
      return;
    }
    setScrollParent(scrollParentOf(box));
    const observer = new ResizeObserver(([entry]) => setWidth(entry.contentRect.width));
    observer.observe(box);
    return () => observer.disconnect();
  }, []);

  const columns = columnCount(width, isMdUp ? 168 : Math.min(width, 144));
  const rows = useMemo(() => {
    const rows: GridRow[] = [];
    if (list.Folders && list.Folders.length > 0) {
      addSection(rows, t("fileManager.folders"), list.Folders, columns);
    }
    if (list.Files.length > 0) {
      addSection(rows, mixedType ? undefined : t("fileManager.files"), list.Files, columns);
    }
    return rows;
  }, [list, columns, mixedType, t]);

  const setRefs = useCallback(
    (element: HTMLElement | null) => {
      boxRef.current = element;
      if (typeof ref == "function") {
        ref(element);
      } else if (ref) {
        ref.current = element;
      }
    },
    [ref],
  );

  return (
    <Box
      ref={setRefs}
      {...rest}
      sx={{
        p: { xs: 1.5, sm: 2 },
      }}
    >
      {scrollParent && width > 0 && (
        <Virtuoso
          customScrollParent={scrollParent}
          increaseViewportBy={200}
          // Fylvo: the first rows come with the first render, not one measuring pass later.
          initialItemCount={Math.min(rows.length, 10)}
          data={rows}
          computeItemKey={(_index, row) => ("title" in row ? `title-${row.title}` : `row-${row.cells[0].key}`)}
          itemContent={(_index, row) =>
            "title" in row ? (
              <Typography
                component="h2"
                variant="caption"
                color="text.secondary"
                fontWeight={600}
                sx={{ display: "block", pb: 1, pt: row.first ? 0 : 2 }}
              >
                {row.title}
              </Typography>
            ) : (
              <Box
                sx={{
                  display: "grid",
                  gridTemplateColumns: `repeat(${columns}, minmax(0, 1fr))`,
                  gap: `${gridGap}px`,
                  pb: row.last ? 0 : `${gridGap}px`,
                }}
              >
                {row.cells}
              </Box>
            )
          }
        />
      )}
    </Box>
  );
});

export default GridView;
