import { CheckCircle } from "@mui/icons-material";
import { Box, Fade, IconButton, ImageListItem, ImageListItemBar, styled } from "@mui/material";
import React, { memo, useCallback, useEffect, useState } from "react";
import { TransitionGroup } from "react-transition-group";
import { FileType, Metadata } from "../../../../api/explorer.ts";
import { hoverSurface, selectedSurface } from "../../../../fylvoTheme.ts";
import { useAppDispatch } from "../../../../redux/hooks.ts";
import { fileIconClicked, loadFileThumb } from "../../../../redux/thunks/file.ts";
import { navigateReconcile } from "../../../../redux/thunks/filemanager.ts";
import CheckUnchecked from "../../../Icons/CheckUnchecked.tsx";
import { FileBlockProps } from "../Explorer.tsx";
import FileIcon from "../FileIcon.tsx";
import {
  LargeIconContainer,
  ThumbBox,
  ThumbBoxContainer,
  ThumbLoadingPlaceholder,
  useFileBlockState,
} from "../GridView/GridFile.tsx";

const StyledImageListItem = styled(ImageListItem, {
  shouldForwardProp: (prop) => !["selected", "transparent", "disabled", "isDropOver"].includes(String(prop)),
})<{
  selected: boolean;
  transparent?: boolean;
  disabled?: boolean;
  isDropOver?: boolean;
}>(({ selected, transparent, isDropOver, disabled, theme }) => {
  return {
    opacity: transparent || disabled ? 0.5 : 1,
    pointerEvents: disabled ? "none" : "auto",
    cursor: "pointer",
    overflow: "hidden",
    borderRadius: theme.shape.borderRadius,
    border: `1px solid ${isDropOver ? theme.palette.primary.main : theme.palette.divider}`,
    backgroundColor: selected || isDropOver ? selectedSurface(theme) : hoverSurface(theme),
    "&:hover": {
      backgroundColor: selectedSurface(theme),
      borderColor: isDropOver ? theme.palette.primary.main : theme.palette.text.disabled,
    },
  };
});

const GalleryImage = memo((props: FileBlockProps) => {
  const { file, isDragging, isDropOver } = props;
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
  } = useFileBlockState(props);

  const [hovered, setHovered] = useState(false);

  // undefined: not loaded, null: no thumb
  const [thumbSrc, setThumbSrc] = useState<string | undefined | null>(noThumb ? null : undefined);
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

    if (file.type == FileType.folder) {
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

  const onIconClick = useCallback(
    (e: React.MouseEvent<HTMLElement>) => {
      e.stopPropagation();
      return dispatch(fileIconClicked(fmIndex, file, e));
    },
    [file, dispatch],
  );

  return (
    <StyledImageListItem
      onClick={file.type == FileType.folder ? onClick : onDoubleClicked}
      selected={!!isSelected}
      transparent={isDragging || fileDisabled || isCut}
      isDropOver={isDropOver && !isDragging}
      disabled={disabled}
      onContextMenu={onContextMenu}
      ref={setRefFunc}
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
    >
      <TransitionGroup style={{ height: "100%" }}>
        {thumbSrc && (
          <Fade key={"image"}>
            <Box>
              <ThumbBoxContainer
                sx={{
                  p: isSelected ? "4px" : 0,
                }}
              >
                <ThumbBox
                  sx={{
                    borderRadius: isSelected ? "4px" : 0,
                  }}
                  loaded={!imageLoading}
                  src={thumbSrc}
                  onLoad={() => setImageLoading(false)}
                  onError={onImgLoadError}
                />
              </ThumbBoxContainer>
            </Box>
          </Fade>
        )}
        {(thumbSrc === undefined || (thumbSrc && imageLoading)) && (
          <Fade key={"loading"}>
            <ThumbLoadingPlaceholder
              sx={{
                borderRadius: 0,
              }}
              ref={isLoadingIndicator ? undefined : ref}
              variant={"rectangular"}
              height={"100%"}
            />
          </Fade>
        )}
        {thumbSrc === null && (
          <Fade key={"icon"}>
            <LargeIconContainer sx={{ bgcolor: "transparent" }}>
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
      <Fade in={!isLoadingIndicator && (hovered || !!isSelected)}>
        <ImageListItemBar
          sx={{
            background: "transparent",
            width: "fit-content",
          }}
          position="top"
          actionIcon={
            <IconButton
              onClick={onIconClick}
              size="small"
              sx={{
                m: "5px",
                p: "4px",
                color: isSelected ? "primary.main" : "text.secondary",
                border: 1,
                borderColor: "divider",
                borderRadius: "6px",
                bgcolor: "background.paper",
                "&:hover": { bgcolor: selectedSurface },
                "&.Mui-focusVisible": { outline: "1px solid", outlineColor: "primary.main" },
              }}
            >
              <TransitionGroup
                style={{
                  width: 16,
                  height: 16,
                }}
              >
                {!isSelected && (
                  <Fade>
                    <Box sx={{ position: "absolute" }}>
                      <CheckUnchecked fontSize={"small"} />
                    </Box>
                  </Fade>
                )}
                {isSelected && (
                  <Fade>
                    <Box sx={{ position: "absolute" }}>
                      <CheckCircle fontSize={"small"} />
                    </Box>
                  </Fade>
                )}
              </TransitionGroup>
            </IconButton>
          }
          actionPosition="left"
        />
      </Fade>
    </StyledImageListItem>
  );
});

export default GalleryImage;
