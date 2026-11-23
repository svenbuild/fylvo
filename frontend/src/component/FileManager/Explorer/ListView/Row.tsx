import { Box, Skeleton, styled } from "@mui/material";
import { memo, useCallback, useEffect } from "react";
import { hoverSurface, selectedSurface } from "../../../../fylvoTheme.ts";
import { useAppDispatch } from "../../../../redux/hooks.ts";
import { navigateReconcile } from "../../../../redux/thunks/filemanager.ts";
import { NoWrapTypography } from "../../../Common/StyledComponents.tsx";
import useActionDisplayOpt from "../../ContextMenu/useActionDisplayOpt.ts";
import { FileBlockProps } from "../Explorer.tsx";
import { useFileBlockState } from "../GridView/GridFile.tsx";
import Cell from "./Cell.tsx";
import { ColumType, listColumnStyle } from "./Column.tsx";

const RowContainer = styled(Box, {
  shouldForwardProp: (prop) => !["selected", "transparent", "isDropOver", "disabled"].includes(String(prop)),
})<{
  selected: boolean;
  transparent?: boolean;
  isDropOver?: boolean;
  disabled?: boolean;
}>(({ theme, disabled, transparent, isDropOver, selected }) => {
  return {
    // Fylvo: the row height follows the density of the account (see applyDensity).
    minHeight: "var(--fylvo-row-height, 32px)",
    "@media (max-width: 599.95px), (pointer: coarse)": { minHeight: "max(40px, var(--fylvo-row-height, 32px))" },
    paddingRight: 32,
    borderBottom: `1px solid ${theme.palette.divider}`,
    display: "flex",
    backgroundColor: selected || isDropOver ? selectedSurface(theme) : "transparent",
    "&:hover": {
      backgroundColor: selected || isDropOver ? selectedSurface(theme) : hoverSurface(theme),
    },
    pointerEvents: disabled ? "none" : "auto",
    opacity: transparent || disabled ? 0.5 : 1,
    outline: isDropOver ? `1px solid ${theme.palette.primary.main}` : undefined,
    outlineOffset: -1,
  };
});

const Row = memo((props: FileBlockProps) => {
  const { file, columns, search, isDragging, isDropOver, showThumb } = props;
  const dispatch = useAppDispatch();

  const {
    fmIndex,
    isSelected,
    isLoadingIndicator,
    noThumb,
    uploading,
    ref,
    inView,
    fileTag,
    onClick,
    onDoubleClicked,
    hoverStateOff,
    hoverStateOn,
    onContextMenu,
    setRefFunc,
    disabled,
    fileDisabled,
    isCut,
    thumbWidth,
    thumbHeight,
  } = useFileBlockState(props);

  const targetDisplayOptions = useActionDisplayOpt([file]);
  const nameMeta = {
    size: !columns?.some((column) => column.type === ColumType.size),
    modified: !columns?.some((column) => column.type === ColumType.date_modified),
  };

  useEffect(() => {
    if (!inView) {
      return;
    }

    if (isLoadingIndicator) {
      if (file.first) {
        dispatch(navigateReconcile(fmIndex, { next_page: true }));
      }
      return;
    }
  }, [inView]);

  const stopPop = useCallback((e: React.MouseEvent<HTMLElement>) => {
    e.stopPropagation();
  }, []);

  return (
    <RowContainer
      transparent={isDragging || fileDisabled || isCut}
      isDropOver={isDropOver && !isDragging}
      ref={setRefFunc}
      selected={!!isSelected}
      onMouseDown={stopPop}
      onClick={onClick}
      onDoubleClick={onDoubleClicked}
      onMouseEnter={hoverStateOn}
      onMouseLeave={hoverStateOff}
      onContextMenu={onContextMenu}
      disabled={disabled}
    >
      {columns?.map((column, index) => (
        <Box key={index} sx={{ ...listColumnStyle(column), display: "flex", alignItems: "center" }}>
          <NoWrapTypography
            sx={{
              width: "100%",
              color: column.type === ColumType.name ? "text.primary" : "text.secondary",
            }}
            variant={"body2"}
          >
            {!file.placeholder && (
              <Cell
                actionDisplayOpt={targetDisplayOptions}
                isSelected={!!isSelected}
                search={search}
                column={column}
                file={file}
                uploading={uploading}
                fileTag={fileTag}
                noThumb={noThumb}
                showThumb={showThumb}
                nameMeta={nameMeta}
                thumbWidth={thumbWidth}
                thumbHeight={thumbHeight}
              />
            )}

            {file.placeholder && <Skeleton variant={"text"} width={0.5 * (column.width ?? column.defaults.width)} />}
          </NoWrapTypography>
        </Box>
      ))}
    </RowContainer>
  );
});

export default Row;
