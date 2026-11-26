import { alpha, Box, Fade, Skeleton, styled } from "@mui/material";
import React, { memo, useCallback, useContext, useEffect, useState } from "react";
import { useInView } from "react-intersection-observer";
import { TransitionGroup } from "react-transition-group";
import { useAppDispatch, useAppSelector } from "../../../../redux/hooks.ts";
import { cachedFileThumb, fileIconClicked, loadFileThumb } from "../../../../redux/thunks/file.ts";
import CheckmarkCircle from "../../../Icons/CheckmarkCircle.tsx";
import CheckUnchecked from "../../../Icons/CheckUnchecked.tsx";
import { FmIndexContext } from "../../FmIndexContext.tsx";
import FileIcon, { isShareMarked, ShareMarker } from "../FileIcon.tsx";
import { FmFile } from "../GridView/GridView.tsx";

const ThumbSquareSize = 24;

const ThumbSquare = styled(Box)(() => ({
  position: "relative",
  width: ThumbSquareSize,
  height: ThumbSquareSize,
  borderRadius: 6,
  overflow: "hidden",
  flexShrink: 0,
  cursor: "pointer",
}));

const ThumbSquareImg = styled("img")<{ loaded: boolean }>(({ theme, loaded }) => ({
  display: "block",
  objectFit: "cover",
  width: "100%",
  height: "100%",
  opacity: loaded ? 1 : 0,
  transition: theme.transitions.create("opacity", {
    duration: theme.transitions.duration.short,
  }),
  userSelect: "none",
  WebkitUserDrag: "none",
  MozUserDrag: "none",
  msUserDrag: "none",
}));

const ThumbSquareLayer = styled(Box)(() => ({
  position: "absolute",
  inset: 0,
  width: "100%",
  height: "100%",
}));

const ThumbSquareFallback = styled(ThumbSquareLayer)(() => ({
  display: "flex",
  alignItems: "center",
  justifyContent: "center",
}));

const FileTypeBadge = styled(Box)(({ theme }) => ({
  position: "absolute",
  bottom: 2,
  right: 2,
  width: 14,
  height: 14,
  borderRadius: 4,
  backgroundColor: alpha(theme.palette.background.paper, 0.62),
  display: "flex",
  alignItems: "center",
  justifyContent: "center",
  pointerEvents: "none",
}));

const SelectOverlay = styled(Box)(({ theme }) => ({
  position: "absolute",
  top: 3,
  left: 3,
  width: 18,
  height: 18,
  borderRadius: 4,
  backgroundColor: alpha(theme.palette.background.paper, 0.62),
  display: "flex",
  alignItems: "center",
  justifyContent: "center",
}));

export interface ListThumbnailProps {
  file: FmFile;
  isSelected?: boolean;
  noThumb?: boolean;
  showLock?: boolean;
}

const ListThumbnail = memo(({ file, isSelected, noThumb, showLock }: ListThumbnailProps) => {
  const dispatch = useAppDispatch();
  const fmIndex = useContext(FmIndexContext);
  const hovered = useAppSelector((state) => state.fileManager[fmIndex].multiSelectHovered[file.path]);

  const skipLoad = !!noThumb || !!showLock;
  const { ref, inView } = useInView({
    triggerOnce: true,
    rootMargin: "200px 0px",
    skip: skipLoad,
  });

  // undefined: not loaded, null: no thumb
  const [thumbSrc, setThumbSrc] = useState<string | undefined | null>(() =>
    skipLoad ? null : dispatch(cachedFileThumb(0, file)),
  );
  const [imageLoading, setImageLoading] = useState(true);

  useEffect(() => {
    if (!inView || skipLoad) {
      return;
    }
    let cancelled = false;
    dispatch(loadFileThumb(0, file)).then((src) => {
      if (!cancelled) setThumbSrc(src);
    });
    return () => {
      cancelled = true;
    };
  }, [inView, skipLoad, file, dispatch]);

  const onImgLoadError = useCallback(() => {
    setImageLoading(false);
    setThumbSrc(null);
  }, []);

  const onIconClick = useCallback(
    (e: React.MouseEvent<HTMLElement>) => {
      dispatch(fileIconClicked(fmIndex, file, e));
    },
    [dispatch, file, fmIndex],
  );

  const showSelectOverlay = isSelected || hovered;

  return (
    <ThumbSquare ref={ref} onClick={onIconClick}>
      <TransitionGroup appear={false} component={null}>
        {thumbSrc && (
          <Fade key="image">
            <ThumbSquareLayer>
              <ThumbSquareImg
                loaded={!imageLoading}
                src={thumbSrc}
                draggable={false}
                onLoad={() => setImageLoading(false)}
                onError={onImgLoadError}
              />
            </ThumbSquareLayer>
          </Fade>
        )}
        {(thumbSrc === undefined || (thumbSrc && imageLoading)) && (
          <Fade key="loading">
            <ThumbSquareLayer>
              <Skeleton variant="rectangular" width="100%" height="100%" />
            </ThumbSquareLayer>
          </Fade>
        )}
        {thumbSrc === null && (
          <Fade key="icon">
            <ThumbSquareFallback>
              <FileIcon
                file={file}
                variant={"medium"}
                iconProps={{
                  sx: {
                    fontSize: 16,
                    height: 16,
                    width: 16,
                    display: "flex",
                    alignItems: "center",
                    justifyContent: "center",
                  },
                }}
              />
            </ThumbSquareFallback>
          </Fade>
        )}
      </TransitionGroup>
      {/* Fylvo: the check takes the middle, the type badge would overlap it. */}
      {thumbSrc && !showSelectOverlay && (
        <FileTypeBadge>
          {/* Fylvo: the corner is too small for the type glyph plus its share mark, so a shared
              file shows only the share mark here. */}
          {isShareMarked(file) ? (
            <ShareMarker file={file} size={12} />
          ) : (
            <FileIcon
              file={file}
              variant={"small"}
              sx={{ p: 0, display: "flex", alignItems: "center", justifyContent: "center" }}
              iconProps={{
                sx: {
                  fontSize: 12,
                  height: 12,
                  width: 12,
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "center",
                },
              }}
            />
          )}
        </FileTypeBadge>
      )}
      {showSelectOverlay && (
        <SelectOverlay>
          {isSelected ? (
            <CheckmarkCircle color={"primary"} sx={{ width: 14, height: 14 }} />
          ) : (
            <CheckUnchecked color={"action"} sx={{ width: 14, height: 14 }} />
          )}
        </SelectOverlay>
      )}
    </ThumbSquare>
  );
});

export default ListThumbnail;
