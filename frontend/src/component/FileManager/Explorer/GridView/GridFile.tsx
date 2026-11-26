import {
  Box,
  ButtonBase,
  Fade,
  InputBase,
  Skeleton,
  styled,
  Tooltip,
  Typography,
  useMediaQuery,
  useTheme,
} from "@mui/material";
import { bindPopover } from "material-ui-popup-state";
import { usePopupState } from "material-ui-popup-state/hooks";
import HoverPopover from "material-ui-popup-state/HoverPopover";
import React, { memo, useCallback, useContext, useEffect, useMemo, useState } from "react";
import { useInView } from "react-intersection-observer";
import { TransitionGroup } from "react-transition-group";
import { FileType, Metadata } from "../../../../api/explorer.ts";
import { hoverSurface, selectedSurface } from "../../../../fylvoTheme.ts";
import { bindDelayedHover } from "../../../../hooks/delayedHover.tsx";
import { useAppDispatch, useAppSelector } from "../../../../redux/hooks.ts";
import {
  cachedFileThumb,
  fileClicked,
  fileDoubleClicked,
  loadFileThumb,
  openFileContextMenu,
} from "../../../../redux/thunks/file.ts";
import { fileHovered, navigateReconcile } from "../../../../redux/thunks/filemanager.ts";
import FileIcon from "../FileIcon.tsx";
import FileSmallIcon from "../FileSmallIcon.tsx";
import FileTagSummary from "../FileTagSummary.tsx";
import FavoriteMark from "../FavoriteMark.tsx";
import ConnectedMark, { rowUnsupported } from "../../Connected/ConnectedMark.tsx";
// @ts-ignore
import Highlighter from "react-highlight-words";

import { ContextMenuTypes } from "../../../../redux/fileManagerSlice.ts";
import { FileManagerIndex } from "../../FileManager.tsx";
import { FmIndexContext } from "../../FmIndexContext.tsx";
import { getFileTags } from "../../Sidebar/Tags.tsx";
import { FileBlockProps } from "../Explorer.tsx";
import UploadingTag from "../UploadingTag.tsx";
import { useInlineRename } from "../useInlineRename.ts";

const StyledButtonBase = styled(ButtonBase, {
  shouldForwardProp: (prop) => !["selected", "transparent", "isDropOver"].includes(String(prop)),
})<{
  selected: boolean;
  transparent?: boolean;
  isDropOver?: boolean;
}>(({ theme, transparent, isDropOver, selected }) => ({
  opacity: transparent ? 0.5 : 1,
  borderRadius: theme.shape.borderRadius,
  border: `1px solid ${isDropOver ? theme.palette.primary.main : theme.palette.divider}`,
  backgroundColor: selected || isDropOver ? selectedSurface(theme) : hoverSurface(theme),
  width: "100%",
  display: "flex",
  alignItems: "stretch",
  "&:hover": {
    backgroundColor: selectedSurface(theme),
    borderColor: isDropOver ? theme.palette.primary.main : theme.palette.text.disabled,
  },
  "&.Mui-focusVisible": {
    outline: `1px solid ${theme.palette.primary.main}`,
    outlineOffset: 1,
  },
}));

const Content = styled(Box)(() => ({
  display: "flex",
  flexDirection: "column",
  flexGrow: 1,
  overflow: "hidden",
}));

export const Header = styled(Box)(() => ({
  minHeight: 36,
  padding: "0 8px",
  gap: 8,
  "@media (max-width: 599.95px), (pointer: coarse)": { minHeight: 40 },
  display: "flex",
  justifyContent: "left",
  alignItems: "center",
  width: "100%",
}));

const ThumbContainer = styled(Box)(({ theme }) => ({
  borderRadius: 6,
  height: 72,
  border: `1px solid ${theme.palette.divider}`,
  overflow: "hidden",
  margin: `0 ${theme.spacing(1)} ${theme.spacing(1)} ${theme.spacing(1)}`,
  position: "relative",
}));

export const FileNameText = styled(Typography)(() => ({
  flexGrow: 1,
  textAlign: "left",
  textOverflow: "ellipsis",
  whiteSpace: "nowrap",
  overflow: "hidden",
  minWidth: 0,
}));

export const ThumbBoxContainer = styled(Box)(() => ({
  position: "absolute",
  top: 0,
  left: 0,
  width: "100%",
  height: "100%",
}));

export const ThumbBox = styled("img")<{ loaded: boolean }>(({ theme, loaded }) => ({
  objectFit: "cover",
  width: "100%",
  height: "100%",
  transition: theme.transitions.create(["opacity", "border-radius"], {
    easing: theme.transitions.easing.easeInOut,
    duration: theme.transitions.duration.standard,
  }),
  opacity: loaded ? 1 : 0,
  userSelect: "none",
  WebkitUserDrag: "none",
  MozUserDrag: "none",
  msUserDrag: "none",
}));

export const ThumbLoadingPlaceholder = styled(Skeleton)(() => ({
  borderRadius: "8px",
  position: "absolute",
  height: "100%",
  width: "100%",
}));

export const LargeIconContainer = styled(Box)(({ theme }) => ({
  display: "flex",
  justifyContent: "center",
  alignItems: "center",
  height: "100%",
  backgroundColor: theme.palette.background.default,
}));

export const ThumbPopoverImg = styled("img")<{ width?: number; height?: number }>(({ width, height }) => ({
  display: "block",
  maxWidth: width ?? "initial",
  maxHeight: height ?? "initial",
  objectFit: "contain",
  width: "auto",
  height: "auto",
  userSelect: "none",
  WebkitUserDrag: "none",
  MozUserDrag: "none",
  msUserDrag: "none",
}));

export const useFileBlockState = (props: FileBlockProps) => {
  const { file, search, dragRef } = props;
  const dispatch = useAppDispatch();
  const isTouch = useMediaQuery("(pointer: coarse)");
  const fmIndex = useContext(FmIndexContext);
  const isSelected = useAppSelector((state) => state.fileManager[fmIndex].selected[file.path]);
  // Fylvo: taken with Ctrl+X, shown paler until it is pasted.
  const isCut = useAppSelector(
    (state) =>
      !!state.globalState.fileClipboard?.cut && state.globalState.fileClipboard.files.some((f) => f.path == file.path),
  );
  const thumbWidth = useAppSelector((state) => state.siteConfig.explorer.config.thumbnail_width);
  const thumbHeight = useAppSelector((state) => state.siteConfig.explorer.config.thumbnail_height);
  const isLoadingIndicator = file.placeholder;
  const noThumb =
    (file.type == FileType.folder || (file.metadata && file.metadata[Metadata.thumbDisabled] != undefined)) &&
    !isLoadingIndicator;
  const uploading = file.metadata && file.metadata[Metadata.upload_session_id] != undefined;
  const { ref, inView } = useInView({
    triggerOnce: true,
    rootMargin: "200px 0px",
    skip: noThumb,
  });
  const fileTag = useMemo(() => getFileTags(file), [file]);

  const onClick = useCallback(
    (e: React.MouseEvent<HTMLElement>) => {
      e.stopPropagation();
      if (!isLoadingIndicator) {
        dispatch(fileClicked(fmIndex, file, e));
      }
    },
    [file, dispatch, fmIndex, isLoadingIndicator],
  );

  const onDoubleClicked = useCallback(
    (e: React.MouseEvent<HTMLElement>) => {
      e.stopPropagation();
      if (!isLoadingIndicator) {
        dispatch(fileDoubleClicked(fmIndex, file, e));
      }
    },
    [file, dispatch, fmIndex, isLoadingIndicator],
  );

  const setHoverState = useCallback(
    (hovered: boolean) => {
      dispatch(fileHovered(fmIndex, file, hovered));
    },
    [dispatch, fmIndex, file],
  );

  const hoverStateOff = useCallback(() => {
    if (!isTouch) {
      setHoverState(false);
    }
  }, [setHoverState, isTouch]);
  const hoverStateOn = useCallback(() => {
    if (!isTouch) {
      setHoverState(true);
    }
  }, [setHoverState, isTouch]);

  const onContextMenu = useCallback(
    (e: React.MouseEvent<HTMLElement>) => {
      e.preventDefault();
      dispatch(
        openFileContextMenu(fmIndex, file, false, e, !search ? ContextMenuTypes.file : ContextMenuTypes.searchResult),
      );
    },
    [dispatch, file, fmIndex, search],
  );

  const setRefFunc = useCallback(
    (e: HTMLElement | null) => {
      if (isLoadingIndicator) {
        ref(e);
      }

      if (dragRef) {
        dragRef(e);
      }
    },
    [dragRef, isLoadingIndicator, ref],
  );

  const fileDisabled = fmIndex == FileManagerIndex.selector && file.type == FileType.file;
  const disabled = isLoadingIndicator || fileDisabled;

  return {
    isCut,
    onClick,
    fmIndex,
    isSelected,
    isLoadingIndicator,
    noThumb,
    uploading,
    ref,
    inView,
    fileTag,
    onDoubleClicked,
    hoverStateOff,
    hoverStateOn,
    onContextMenu,
    setRefFunc,
    disabled,
    fileDisabled,
    thumbWidth,
    thumbHeight,
  };
};

const GridFile = memo((props: FileBlockProps) => {
  const { file, isDragging, isDropOver, search, showThumb, index, dragRef } = props;
  const dispatch = useAppDispatch();
  const theme = useTheme();
  const isTouch = useMediaQuery("(pointer: coarse)");
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

  const {
    isEditing,
    editValue,
    inputRef,
    onNameClick,
    onNameDoubleClick,
    onInputKeyDown,
    onInputBlur,
    onInputChange,
    stopPropagation,
  } = useInlineRename({ file, isSelected: !!isSelected, uploading, isTouch });

  const popupState = usePopupState({
    variant: "popover",
    popupId: "thumbPreview" + file.id,
  });

  // undefined: not loaded, null: no thumb
  const [thumbSrc, setThumbSrc] = useState<string | undefined | null>(() =>
    noThumb ? null : dispatch(cachedFileThumb(0, file)),
  );
  const [imageLoading, setImageLoading] = useState(true);

  const onImgLoadError = useCallback(() => {
    setImageLoading(false);
    setThumbSrc(null);
  }, [setImageLoading, setThumbSrc]);

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

    if (!showThumb || file.type == FileType.folder) {
      return;
    }

    if (file.metadata && file.metadata[Metadata.thumbDisabled] !== undefined) {
      // No thumb available
      setThumbSrc(null);
      return;
    }

    // Fylvo: loaded again for a new version of the file; the answer for an older one is dropped.
    let cancelled = false;
    dispatch(loadFileThumb(0, file)).then((src) => {
      if (!cancelled) setThumbSrc(src);
    });
    return () => {
      cancelled = true;
    };
  }, [inView, file.primary_entity]);

  const hoverProps = bindDelayedHover(popupState, 800);
  const { open: thumbPopoverOpen, ...rest } = bindPopover(popupState);

  const stopPop = useCallback((e: React.MouseEvent<HTMLElement>) => {
    e.stopPropagation();
  }, []);

  return (
    <>
      <StyledButtonBase
        onDoubleClick={onDoubleClicked}
        transparent={isDragging || fileDisabled || isCut}
        isDropOver={isDropOver && !isDragging}
        onContextMenu={onContextMenu}
        data-rect-id={index ?? ""}
        selected={!!isSelected}
        disabled={disabled}
        onClick={onClick}
        ref={setRefFunc}
        onMouseDown={stopPop}
        onMouseEnter={hoverStateOn}
        onMouseLeave={hoverStateOff}
      >
        <Content>
          <Header
            onClick={isEditing ? stopPropagation : undefined}
            onDoubleClick={isEditing ? stopPropagation : undefined}
            onMouseDown={isEditing ? stopPropagation : undefined}
            onMouseMove={isEditing ? stopPropagation : undefined}
            onDragStart={isEditing ? stopPropagation : undefined}
          >
            <FileSmallIcon selected={!!isSelected} file={file} loading={isLoadingIndicator} />
            {!isLoadingIndicator && isEditing && (
              <InputBase
                inputRef={inputRef}
                value={editValue}
                onChange={onInputChange}
                onKeyDown={onInputKeyDown}
                onBlur={onInputBlur}
                size="small"
                fullWidth
                sx={{
                  flex: 1,
                  minWidth: 0,
                  fontSize: "inherit",
                  "& .MuiInputBase-input": {
                    py: 0,
                    px: 0.5,
                    border: `1px solid ${theme.palette.primary.main}`,
                    borderRadius: 0.5,
                  },
                }}
              />
            )}
            {!isLoadingIndicator && !isEditing && (
              <Tooltip title={file.name}>
                <FileNameText
                  variant="body2"
                  onClick={onNameClick}
                  onDoubleClick={onNameDoubleClick}
                  sx={rowUnsupported(file) ? { opacity: 0.6 } : undefined}
                >
                  {search?.name ? (
                    <Highlighter
                      highlightClassName="highlight-marker"
                      searchWords={search?.name}
                      autoEscape={true}
                      textToHighlight={file.name}
                    />
                  ) : (
                    file.name
                  )}
                </FileNameText>
              </Tooltip>
            )}
            {!isEditing && !isLoadingIndicator && <FavoriteMark file={file} />}
            {!isEditing && !isLoadingIndicator && <ConnectedMark file={file} />}
            {!isEditing && !uploading && fileTag && fileTag.length > 0 && (
              <FileTagSummary sx={{ maxWidth: "50%" }} tags={fileTag} />
            )}
            {!isEditing && uploading && <UploadingTag sx={{ maxWidth: "50%" }} />}
            {isLoadingIndicator && (
              <Skeleton
                variant="text"
                sx={{
                  fontVariant: "body2",
                  width: "100%",
                  mr: 1,
                }}
              />
            )}
          </Header>
          {showThumb && (
            // Fylvo: watched also when a known preview or icon shows at once (see cachedFileThumb).
            <ThumbContainer ref={isLoadingIndicator ? undefined : ref}>
              <TransitionGroup appear={false} style={{ height: "100%" }}>
                {thumbSrc && (
                  <Fade key={"image"}>
                    <ThumbBoxContainer>
                      <ThumbBox
                        loaded={!imageLoading}
                        src={thumbSrc}
                        onLoad={() => setImageLoading(false)}
                        onError={onImgLoadError}
                        {...(isTouch ? {} : hoverProps)}
                      />
                    </ThumbBoxContainer>
                  </Fade>
                )}
                {(thumbSrc === undefined || (thumbSrc && imageLoading)) && (
                  <Fade key={"loading"}>
                    <ThumbLoadingPlaceholder variant={"rectangular"} height={"100%"} />
                  </Fade>
                )}
                {thumbSrc === null && (
                  <Fade key={"icon"}>
                    <LargeIconContainer>
                      <FileIcon
                        variant="preview"
                        iconProps={{
                          sx: {
                            fontSize: 30,
                            height: 30,
                            width: 30,
                          },
                        }}
                        file={file}
                        loading={isLoadingIndicator}
                      />
                    </LargeIconContainer>
                  </Fade>
                )}
              </TransitionGroup>
            </ThumbContainer>
          )}
        </Content>
        {thumbSrc && showThumb && (
          <HoverPopover
            open={thumbPopoverOpen}
            // Fylvo: the preview only shows; the focus stays on the card, so the keys keep working.
            disableAutoFocus
            disableEnforceFocus
            disableRestoreFocus
            sx={{
              zIndex: (t) => t.zIndex.drawer,
            }}
            anchorOrigin={{
              vertical: "center",
              horizontal: "center",
            }}
            transformOrigin={{
              vertical: "center",
              horizontal: "center",
            }}
            {...rest}
          >
            <ThumbPopoverImg src={thumbSrc} draggable={false} width={thumbWidth} height={thumbHeight} />
          </HoverPopover>
        )}
      </StyledButtonBase>
    </>
  );
});

export default GridFile;
