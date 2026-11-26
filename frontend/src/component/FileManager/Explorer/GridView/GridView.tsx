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
import { AreaCandidate, AreaLayout } from "../../../../hooks/areaSelection.ts";

export interface GridViewProps {
  layoutRef?: AreaLayout;
  [key: string]: any;
}

export interface FmFile extends FileResponse {
  id: string;
  first?: boolean;
  placeholder?: boolean;
}

// A card and its place in the file list (none for the "loading" placeholders).
interface Cell {
  element: JSX.Element;
  index?: number;
}

interface listComponents {
  Folders?: Cell[];
  Files: Cell[];
}

const gridGap = 8;

// Fylvo: the grid shows only the rows near the visible part (a folder can hold thousands of files).
// The rows are cut to as many columns as `repeat(auto-fill, minmax(<min>, 1fr))` would make.
function columnCount(width: number, minWidth: number) {
  return Math.max(1, Math.floor((width + gridGap) / (minWidth + gridGap)));
}

type GridRow = { title: string; first: boolean } | { cells: Cell[]; last: boolean; section: number };

function addSection(rows: GridRow[], title: string | undefined, cells: Cell[], columns: number, section: number) {
  if (title !== undefined) {
    rows.push({ title, first: rows.length == 0 });
  }
  for (let i = 0; i < cells.length; i += columns) {
    rows.push({ cells: cells.slice(i, i + columns), last: i + columns >= cells.length, section });
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

const GridView = React.forwardRef(({ layoutRef, ...rest }: GridViewProps, ref) => {
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
          list.Folders.push({
            index,
            element: (
              <Box key={file.id} sx={{ minWidth: 0 }}>
                <DndWrappedFile
                  component={GridFile}
                  search={search_params}
                  index={index}
                  showThumb={mixedType}
                  file={file}
                />
              </Box>
            ),
          });
        } else {
          list.Files.push({
            index,
            element: (
              <Box key={file.id} sx={{ minWidth: 0 }}>
                <DndWrappedFile
                  component={GridFile}
                  search={search_params}
                  index={index}
                  showThumb={showThumb}
                  file={file}
                />
              </Box>
            ),
          });
        }
      });

      // Add loading placeholder if there is next page
      if (pagination && pagination.next_token) {
        for (let i = 0; i < loadingPlaceHolderNumb; i++) {
          const id = `loadingPlaceholder-${pagination.next_token}-${i}`;
          const loadingPlaceholder = {
            element: (
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
            ),
          };
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
    const style = getComputedStyle(box);
    setWidth(box.clientWidth - parseFloat(style.paddingLeft) - parseFloat(style.paddingRight));
    const observer = new ResizeObserver(([entry]) => setWidth(entry.contentRect.width));
    observer.observe(box);
    return () => observer.disconnect();
  }, []);

  const columns = columnCount(width, isMdUp ? 168 : Math.min(width, 144));
  const rows = useMemo(() => {
    const rows: GridRow[] = [];
    if (list.Folders && list.Folders.length > 0) {
      addSection(rows, t("fileManager.folders"), list.Folders, columns, 0);
    }
    if (list.Files.length > 0) {
      addSection(rows, mixedType ? undefined : t("fileManager.files"), list.Files, columns, 1);
    }
    return rows;
  }, [list, columns, mixedType, t]);

  // Fylvo: where every card is, also the ones not drawn, for the area selection (useAreaSelection):
  // places in the scrolled content of the scroll parent. Rows of a kind have the same height; the
  // heights are taken from the rows drawn now (or earlier), the drawn rows anchor the positions.
  const heights = useRef<{ title?: number; titleGap?: number; cards: number[] }>({ cards: [] });
  const layout = useCallback((): AreaCandidate[][] => {
    const box = boxRef.current;
    if (!box || !scrollParent) {
      return [];
    }
    const parentBox = scrollParent.getBoundingClientRect();
    const known = heights.current;
    let anchor: { row: number; top: number } | undefined;
    box.querySelectorAll<HTMLElement>("[data-index]").forEach((item) => {
      const rowIndex = Number(item.dataset.index);
      const row = rows[rowIndex];
      if (!row) {
        return;
      }
      if ("title" in row) {
        const gap = parseFloat(getComputedStyle(item.firstElementChild ?? item).paddingTop);
        known.title = item.offsetHeight - (row.first ? 0 : gap);
        if (!row.first) {
          known.titleGap = gap;
        }
      } else {
        const card = item.querySelector("[data-rect-id]");
        if (card) {
          known.cards[row.section] = card.getBoundingClientRect().height;
        }
      }
      anchor ??= { row: rowIndex, top: scrollParent.scrollTop + item.getBoundingClientRect().top - parentBox.top };
    });
    if (!anchor) {
      return [];
    }

    const tops: number[] = [];
    let y = 0;
    rows.forEach((row) => {
      tops.push(y);
      if ("title" in row) {
        y += (known.title ?? 0) + (row.first ? 0 : known.titleGap ?? 0);
      } else {
        y += (known.cards[row.section] ?? known.cards[1 - row.section] ?? 0) + (row.last ? 0 : gridGap);
      }
    });
    const shift = anchor.top - tops[anchor.row];
    const left = box.getBoundingClientRect().left - parentBox.left + parseFloat(getComputedStyle(box).paddingLeft);
    const cellWidth = (width - (columns - 1) * gridGap) / columns;
    const result: AreaCandidate[][] = [];
    rows.forEach((row, rowIndex) => {
      if ("title" in row) {
        return;
      }
      const top = tops[rowIndex] + shift;
      const height = known.cards[row.section] ?? known.cards[1 - row.section] ?? 0;
      const cells: AreaCandidate[] = [];
      row.cells.forEach((cell, column) => {
        if (cell.index !== undefined) {
          const x = left + column * (cellWidth + gridGap);
          cells.push({ index: String(cell.index), x, y: top, right: x + cellWidth, bottom: top + height });
        }
      });
      if (cells.length > 0) {
        result.push(cells);
      }
    });
    return result;
  }, [rows, scrollParent, width, columns]);
  useLayoutEffect(() => {
    if (!layoutRef) {
      return;
    }
    layoutRef.current = layout;
    return () => {
      layoutRef.current = undefined;
    };
  }, [layout, layoutRef]);

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
                {row.cells.map((cell) => cell.element)}
              </Box>
            )
          }
        />
      )}
    </Box>
  );
});

export default GridView;
