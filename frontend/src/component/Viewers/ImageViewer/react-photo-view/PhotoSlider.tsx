import { Box, IconButton, Tooltip, useMediaQuery, useTheme } from "@mui/material";
import { hoverSurface, shellSurface } from "../../../../fylvoTheme.ts";
import React, { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { setSidebar } from "../../../../redux/globalStateSlice.ts";
import { useAppDispatch, useAppSelector } from "../../../../redux/hooks.ts";
import { downloadSingleFile } from "../../../../redux/thunks/download.ts";
import { openFileContextMenu } from "../../../../redux/thunks/file.ts";
import { switchToImageEditor } from "../../../../redux/thunks/viewer.ts";
import { fileExtension } from "../../../../util";
import useActionDisplayOpt, { canUpdate } from "../../../FileManager/ContextMenu/useActionDisplayOpt.ts";
import { FileManagerIndex } from "../../../FileManager/FileManager.tsx";
import Dismiss from "../../../Icons/Dismiss.tsx";
import Download from "../../../Icons/Download.tsx";
import ImageEdit from "../../../Icons/ImageEdit.tsx";
import Info from "../../../Icons/Info.tsx";
import MoreHorizontal from "../../../Icons/MoreHorizontal.tsx";
import { editorSupportedExt } from "../editorExt.ts";
import ArrowLeft from "./components/ArrowLeft";
import ArrowRight from "./components/ArrowRight";
import SlidePortal from "./components/SlidePortal";
import useAdjacentImages from "./hooks/useAdjacentImages";
import useAnimationVisible from "./hooks/useAnimationVisible";
import useEventListener from "./hooks/useEventListener";
import useIsomorphicLayoutEffect from "./hooks/useIsomorphicLayoutEffect";
import useMethods from "./hooks/useMethods";
import useSetState from "./hooks/useSetState";
import PhotoBox from "./PhotoBox";
import "./PhotoSlider.less";
import type { DataType, OverlayRenderProps, PhotoProviderBase, ReachType } from "./types";
import isTouchDevice from "./utils/isTouchDevice";
import { limitNumber } from "./utils/limitTarget";
import { defaultEasing, defaultOpacity, defaultSpeed, horizontalOffset, maxMoveOffset } from "./variables";

export interface IPhotoSliderProps extends PhotoProviderBase {
  // image list
  images: DataType[];
  // current image index
  index?: number;
  // index change callback
  onIndexChange?: (index: number) => void;
  // visible
  visible: boolean;
  // close callback
  onClose: (evt?: React.MouseEvent | React.TouchEvent) => void;
  // callback after the close animation ends
  afterClose?: () => void;
  moreFiles?: boolean;
}

type PhotoSliderState = {
  // offset
  x: number;
  // image is being touched
  touched: boolean;
  // whether to pause the transition
  pause: boolean;
  // x coordinate when Reach starts
  lastCX: number | undefined;
  // y coordinate when Reach starts
  lastCY: number | undefined;
  // background opacity
  bg: number | null | undefined;
  // background opacity at the last close
  lastBg: number | null | undefined;
  // whether to show the overlay
  overlay: boolean;
  // whether in the minimal state (can be pulled down to close)
  minimal: boolean;
  // scale
  scale: number;
  // rotation
  rotate: number;
  // scale callback
  onScale?: (scale: number) => void;
  // rotate callback
  onRotate?: (rotate: number) => void;
};

const initialState: PhotoSliderState = {
  x: 0,
  touched: false,
  pause: false,
  lastCX: undefined,
  lastCY: undefined,
  bg: undefined,
  lastBg: undefined,
  overlay: true,
  minimal: true,
  scale: 1,
  rotate: 0,
};

export default function PhotoSlider(props: IPhotoSliderProps) {
  const {
    loop = 3,
    speed: speedFn,
    easing: easingFn,
    photoClosable,
    maskClosable = true,
    maskOpacity = defaultOpacity,
    pullClosable = true,
    bannerVisible = true,
    overlayRender,
    toolbarRender,
    className,
    maskClassName,
    photoClassName,
    photoWrapClassName,
    loadingElement,
    brokenElement,
    images,
    index: controlledIndex = 0,
    onIndexChange: controlledIndexChange,
    visible,
    onClose,
    afterClose,
    portalContainer,
    moreFiles,
  } = props;

  const { t } = useTranslation();
  const theme = useTheme();
  const isMobile = useMediaQuery(theme.breakpoints.down("sm"));
  const dispatch = useAppDispatch();
  const containerRef = useRef(null);
  const sideBarOpen = useAppSelector((state) => state.globalState.sidebarOpen);
  const [state, updateState] = useSetState(initialState);
  const [innerIndex, updateInnerIndex] = useState(0);
  const dynamicInnerWidth = sideBarOpen ? window.innerWidth - 300 : window.innerWidth;

  const {
    x,
    touched,
    pause,

    lastCX,
    lastCY,

    bg = maskOpacity,
    lastBg,
    overlay,
    minimal,

    scale,
    rotate,
    onScale,
    onRotate,
  } = state;

  // controlled index
  const isControlled = props.hasOwnProperty("index");
  const index = isControlled ? controlledIndex : innerIndex;
  const onIndexChange = isControlled ? controlledIndexChange : updateInnerIndex;
  // internal virtual index
  const virtualIndexRef = useRef(index);

  // current image
  const imageLength = images.length;
  const currentImage: DataType | undefined = images[index];

  // whether enabled
  // noinspection SuspiciousTypeOfGuard
  const enableLoop = typeof loop === "boolean" ? loop : imageLength > loop;

  // show animation handling
  const [realVisible, activeAnimation, onAnimationEnd] = useAnimationVisible(visible, afterClose);

  useEffect(() => {
    setTimeout(() => {
      document.body.style.overflow = "hidden";
    }, 500);
    return () => {
      setTimeout(() => {
        document.body.style.overflow = isMobile ? "initial" : "hidden";
      }, 500);
    };
  }, []);

  useIsomorphicLayoutEffect(() => {
    // show the popup layer, fix the correct target
    if (realVisible) {
      updateState({
        pause: true,
        x: index * -(dynamicInnerWidth + horizontalOffset),
      });
      virtualIndexRef.current = index;
      return;
    }
    // reset state after closing
    updateState(initialState);
  }, [realVisible]);

  const { close, changeIndex } = useMethods({
    close(evt?: React.MouseEvent | React.TouchEvent) {
      if (onRotate) {
        onRotate(0);
      }
      updateState({
        overlay: true,
        // remember the opacity at close time
        lastBg: bg,
      });
      onClose(evt);
    },
    changeIndex(nextIndex: number, isPause: boolean = false) {
      // current index
      const currentIndex = enableLoop ? virtualIndexRef.current + (nextIndex - index) : nextIndex;
      const max = imageLength - 1;
      // virtual index
      // non-loop mode, limit the range
      const limitIndex = limitNumber(currentIndex, 0, max);
      const nextVirtualIndex = enableLoop ? currentIndex : limitIndex;
      // width of a single screen
      const singlePageWidth = dynamicInnerWidth + horizontalOffset;

      updateState({
        touched: false,
        lastCX: undefined,
        lastCY: undefined,
        x: -singlePageWidth * nextVirtualIndex,
        pause: isPause,
      });

      virtualIndexRef.current = nextVirtualIndex;
      // update the real index
      const realLoopIndex = nextIndex < 0 ? max : nextIndex > max ? 0 : nextIndex;
      if (onIndexChange) {
        onIndexChange(enableLoop ? realLoopIndex : limitIndex);
      }
    },
  });

  useEventListener("keydown", (evt: KeyboardEvent) => {
    if (visible) {
      switch (evt.key) {
        case "ArrowLeft":
          changeIndex(index - 1, true);
          break;
        case "ArrowRight":
          changeIndex(index + 1, true);
          break;
        case "Escape":
          close();
          break;
        default:
      }
    }
  });

  function handlePhotoTap(closeable: boolean | undefined) {
    return closeable ? close() : updateState({ overlay: !overlay });
  }

  const handleResize = (dynamicInnerWidth: number) => () => {
    updateState({
      x: -(dynamicInnerWidth + horizontalOffset) * index,
      lastCX: undefined,
      lastCY: undefined,
      pause: true,
    });
    virtualIndexRef.current = index;
  };

  function handleReachVerticalMove(clientY: number, nextScale?: number) {
    if (lastCY === undefined) {
      updateState({
        touched: true,
        lastCY: clientY,
        bg,
        minimal: true,
      });
      return;
    }
    const opacity =
      maskOpacity === null ? null : limitNumber(maskOpacity, 0.01, maskOpacity - Math.abs(clientY - lastCY) / 100 / 4);

    updateState({
      touched: true,
      lastCY,
      bg: nextScale === 1 ? opacity : maskOpacity,
      minimal: nextScale === 1,
    });
  }

  function handleReachHorizontalMove(clientX: number) {
    if (lastCX === undefined) {
      updateState({
        touched: true,
        lastCX: clientX,
        x,
        pause: false,
      });
      return;
    }
    const originOffsetClientX = clientX - lastCX;
    let offsetClientX = originOffsetClientX;

    // halve the overshoot distance for the first and last image
    if (
      !enableLoop &&
      ((index === 0 && originOffsetClientX > 0) || (index === imageLength - 1 && originOffsetClientX < 0))
    ) {
      offsetClientX = originOffsetClientX / 2;
    }

    updateState({
      touched: true,
      lastCX,
      x: -(dynamicInnerWidth + horizontalOffset) * virtualIndexRef.current + offsetClientX,
      pause: false,
    });
  }

  function handleReachMove(reachPosition: ReachType, clientX: number, clientY: number, nextScale?: number) {
    if (reachPosition === "x") {
      handleReachHorizontalMove(clientX);
    } else if (reachPosition === "y") {
      handleReachVerticalMove(clientY, nextScale);
    }
  }

  function handleReachUp(clientX: number, clientY: number) {
    const offsetClientX = clientX - (lastCX ?? clientX);
    const offsetClientY = clientY - (lastCY ?? clientY);
    let willClose = false;
    // next image
    if (offsetClientX < -maxMoveOffset) {
      changeIndex(index + 1);
      return;
    }
    // previous image
    if (offsetClientX > maxMoveOffset) {
      changeIndex(index - 1);
      return;
    }
    const singlePageWidth = dynamicInnerWidth + horizontalOffset;
    // current offset
    const currentTranslateX = -singlePageWidth * virtualIndexRef.current;

    if (Math.abs(offsetClientY) > 100 && minimal && pullClosable) {
      willClose = true;
      close();
    }
    updateState({
      touched: false,
      x: currentTranslateX,
      lastCX: undefined,
      lastCY: undefined,
      bg: maskOpacity,
      overlay: willClose ? true : overlay,
    });
  }
  // take the adjacent images
  const adjacentImages = useAdjacentImages(images, index, enableLoop);
  const currentFile = images[index]?.file;
  const displayOpt = useActionDisplayOpt(currentFile ? [currentFile] : []);

  useEffect(() => {
    //handleReachMove("x", 0, 0);
    handleReachUp(0, 0);
  }, [sideBarOpen]);

  if (!realVisible) {
    return null;
  }

  const currentOverlayVisible = overlay && !activeAnimation;
  // use the opacity saved from the pull-down while closing
  const currentOpacity = visible ? bg : lastBg;
  // overlay parameters
  const overlayParams: OverlayRenderProps | undefined = onScale &&
    onRotate && {
      images,
      index,
      visible,
      onClose: close,
      onIndexChange: changeIndex,
      overlayVisible: currentOverlayVisible,
      overlay: currentImage && currentImage.overlay,
      scale,
      rotate,
      onScale,
      onRotate,
    };
  // animation duration
  const currentSpeed = speedFn ? speedFn(activeAnimation) : defaultSpeed;
  const currentEasing = easingFn ? easingFn(activeAnimation) : defaultEasing;
  const slideSpeed = speedFn ? speedFn(3) : defaultSpeed + 200;
  const slideEasing = easingFn ? easingFn(3) : defaultEasing;

  return (
    <Box ref={containerRef}>
      <SlidePortal
        className={`PhotoView-Portal${!currentOverlayVisible ? " PhotoView-Slider__clean" : ""}${
          !visible ? " PhotoView-Slider__willClose" : ""
        }${className ? ` ${className}` : ""}`}
        style={{
          width: sideBarOpen ? "calc(100% - 300px)" : "100%",
        }}
        role="dialog"
        onClick={(e) => e.stopPropagation()}
        container={portalContainer}
      >
        <div
          className={`PhotoView-Slider__Backdrop${maskClassName ? ` ${maskClassName}` : ""}${
            activeAnimation === 1
              ? " PhotoView-Slider__fadeIn"
              : activeAnimation === 2
                ? " PhotoView-Slider__fadeOut"
                : ""
          }`}
          style={{
            background: currentOpacity ? `rgba(0, 0, 0, ${currentOpacity})` : undefined,
            transitionTimingFunction: currentEasing,
            transitionDuration: `${touched ? 0 : currentSpeed}ms`,
            animationDuration: `${currentSpeed}ms`,
          }}
          onAnimationEnd={onAnimationEnd}
        />
        {bannerVisible && (
          <Box
            className="PhotoView-Slider__BannerWrap"
            sx={{
              bgcolor: shellSurface,
              color: "text.secondary",
              borderBottom: 1,
              borderColor: "divider",
              "& .MuiIconButton-root:hover": { bgcolor: hoverSurface },
              "& .MuiIconButton-root.Mui-focusVisible": {
                outline: (theme) => `1px solid ${theme.palette.primary.main}`,
              },
              "@media (max-width: 599.95px), (pointer: coarse)": {
                "& .MuiIconButton-root": { minWidth: 40, minHeight: 40 },
              },
            }}
          >
            <div className="PhotoView-Slider__Counter">
              {index + 1} / {moreFiles ? imageLength - 1 : imageLength}
              {moreFiles ? "+" : ""}
            </div>
            <div className="PhotoView-Slider__BannerRight">
              {toolbarRender && overlayParams && toolbarRender(overlayParams)}
              {currentFile && displayOpt.showDownload && (
                <Tooltip title={t("application:fileManager.download")}>
                  <IconButton onClick={() => dispatch(downloadSingleFile(currentFile, images[index]?.version))}>
                    <Download fontSize={"small"} />
                  </IconButton>
                </Tooltip>
              )}
              {currentFile && displayOpt.showInfo && (
                <Tooltip title={t("application:fileManager.details")}>
                  <IconButton
                    onClick={() =>
                      dispatch(
                        setSidebar({
                          open: true,
                          target: images[index].file,
                        }),
                      )
                    }
                  >
                    <Info fontSize={"small"} />
                  </IconButton>
                </Tooltip>
              )}
              {currentFile &&
                displayOpt &&
                canUpdate(displayOpt) &&
                editorSupportedExt.includes(fileExtension(currentFile.name) ?? "") && (
                  <Tooltip title={t("application:fileManager.edit")}>
                    <IconButton onClick={() => dispatch(switchToImageEditor(currentFile, images[index]?.version))}>
                      <ImageEdit fontSize={"small"} />
                    </IconButton>
                  </Tooltip>
                )}
              {currentFile && (
                <Tooltip title={t("application:fileManager.moreActions")}>
                  <IconButton
                    onClick={(e) => dispatch(openFileContextMenu(FileManagerIndex.main, currentFile, false, e))}
                  >
                    <MoreHorizontal fontSize={"small"} />
                  </IconButton>
                </Tooltip>
              )}
              <IconButton onClick={close}>
                <Dismiss fontSize={"small"} />
              </IconButton>
            </div>
          </Box>
        )}
        {adjacentImages.map((item: DataType, currentIndex) => {
          // index position before the cut
          const nextIndex =
            !enableLoop && index === 0 ? index + currentIndex : virtualIndexRef.current - 1 + currentIndex;

          return (
            <PhotoBox
              key={enableLoop ? `${item.key}/${item.src}/${nextIndex}` : item.key}
              item={item}
              speed={currentSpeed}
              easing={currentEasing}
              visible={visible}
              onReachMove={handleReachMove}
              onReachUp={handleReachUp}
              onPhotoTap={() => handlePhotoTap(photoClosable)}
              onMaskTap={() => handlePhotoTap(maskClosable)}
              wrapClassName={photoWrapClassName}
              className={photoClassName}
              style={{
                left: `${(dynamicInnerWidth + horizontalOffset) * nextIndex}px`,
                transform: `translate3d(${x}px, 0px, 0)`,
                transition: touched || pause ? undefined : `transform ${slideSpeed}ms ${slideEasing}`,
              }}
              loadingElement={loadingElement}
              brokenElement={brokenElement}
              onPhotoResize={handleResize(dynamicInnerWidth)}
              isActive={virtualIndexRef.current === nextIndex}
              expose={updateState}
            />
          );
        })}
        {!isTouchDevice && bannerVisible && (
          <>
            {(enableLoop || index !== 0) && (
              <div className="PhotoView-Slider__ArrowLeft" onClick={() => changeIndex(index - 1, true)}>
                <ArrowLeft />
              </div>
            )}
            {(enableLoop || index + 1 < imageLength) && (
              <div className="PhotoView-Slider__ArrowRight" onClick={() => changeIndex(index + 1, true)}>
                <ArrowRight />
              </div>
            )}
          </>
        )}
        {overlayRender && overlayParams && (
          <div className="PhotoView-Slider__Overlay">{overlayRender(overlayParams)}</div>
        )}
      </SlidePortal>
    </Box>
  );
}
