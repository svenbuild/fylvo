import { grey } from "@mui/material/colors";
import React, { useEffect, useRef } from "react";
import { useInView } from "react-intersection-observer";
import { useAppDispatch, useAppSelector } from "../../../../redux/hooks.ts";
import { loadMorePages } from "../../../../redux/thunks/filemanager.ts";
import FacebookCircularProgress from "../../../Common/CircularProgress.tsx";
import { FileManagerIndex } from "../../../FileManager/FileManager.tsx";
import useAnimationPosition from "./hooks/useAnimationPosition";
import useContinuousTap from "./hooks/useContinuousTap";
import useDebounceCallback from "./hooks/useDebounceCallback";
import useEventListener from "./hooks/useEventListener";
import useIsomorphicLayoutEffect from "./hooks/useIsomorphicLayoutEffect";
import useMethods from "./hooks/useMethods";
import useMountedRef from "./hooks/useMountedRef";
import useScrollPosition from "./hooks/useScrollPosition";
import useSetState from "./hooks/useSetState";
import type { IPhotoLoadedParams } from "./Photo";
import Photo from "./Photo";
import "./PhotoBox.less";
import type {
  BrokenElementParams,
  DataType,
  ExposedProperties,
  PhotoTapFunction,
  ReachFunction,
  ReachMoveFunction,
  ReachType,
  TouchStartType,
} from "./types";
import { computePositionEdge, getReachType } from "./utils/edgeHandle";
import getMultipleTouchPosition from "./utils/getMultipleTouchPosition";
import getPositionOnMoveOrScale from "./utils/getPositionOnMoveOrScale";
import getRotateSize from "./utils/getRotateSize";
import getSuitableImageSize from "./utils/getSuitableImageSize";
import isTouchDevice from "./utils/isTouchDevice";
import { limitScale } from "./utils/limitTarget";
import { minStartTouchOffset, scaleBuffer } from "./variables";

export interface PhotoBoxProps {
  // image info
  item: DataType;
  // whether visible
  visible: boolean;
  // animation duration
  speed: number;
  // animation easing function
  easing: string;
  // container class name
  wrapClassName?: string;
  // image class name
  className?: string;
  // style
  style?: object;
  // custom loading element
  loadingElement?: JSX.Element;
  // element shown when loading fails
  brokenElement?: JSX.Element | ((photoProps: BrokenElementParams) => JSX.Element);

  // Photo click event
  onPhotoTap: PhotoTapFunction;
  // Mask click event
  onMaskTap: PhotoTapFunction;
  // swipe event when reaching an edge
  onReachMove: ReachMoveFunction;
  // touch end event
  onReachUp: ReachFunction;
  // Resize event
  onPhotoResize: () => void;
  // export properties to the parent component
  expose: (state: ExposedProperties) => void;
  // whether an operation is in progress
  isActive: boolean;
}

const initialState = {
  // real width
  naturalWidth: undefined as number | undefined,
  // real height
  naturalHeight: undefined as number | undefined,
  // width
  width: undefined as number | undefined,
  // height
  height: undefined as number | undefined,
  // loaded state
  loaded: undefined as boolean | undefined,
  // broken state
  broken: false,

  // image X offset
  x: 0,
  // image y offset
  y: 0,
  // image is being touched
  touched: false,
  // background is being touched
  maskTouched: false,
  // rotation state
  rotate: 0,
  // zoom
  scale: 1,

  // raw x coordinate at touch start
  CX: 0,
  // raw y coordinate at touch start
  CY: 0,

  // image x offset at touch start
  lastX: 0,
  // image y offset at touch start
  lastY: 0,
  // raw x coordinate of the previous touch state
  lastCX: 0,
  // raw y coordinate of the previous touch state
  lastCY: 0,
  // scale of the previous touch state
  lastScale: 1,

  // time at touch start
  touchTime: 0,
  // multi-touch distance
  touchLength: 0,
  // whether to pause the transition
  pause: true,
  // stop Raf
  stopRaf: true,
  // current edge trigger state
  reach: undefined as ReachType,
};

export default function PhotoBox({
  item: {
    render,
    file,
    version,
    width: customWidth = 0,
    height: customHeight = 0,
    originRef,
    loadMorePlaceholder,
    key,
  },
  visible,
  speed,
  easing,
  wrapClassName,
  className,
  style,
  loadingElement,
  brokenElement,

  onPhotoTap,
  onMaskTap,
  onReachMove,
  onReachUp,
  onPhotoResize,
  isActive,
  expose,
}: PhotoBoxProps) {
  const [state, updateState] = useSetState(initialState);
  const initialTouchRef = useRef<TouchStartType>(0);
  const mounted = useMountedRef();

  const {
    naturalWidth = customWidth,
    naturalHeight = customHeight,
    width = customWidth,
    height = customHeight,
    loaded = !file,
    broken,
    x,
    y,
    touched,
    stopRaf,
    maskTouched,
    rotate,
    scale,
    CX,
    CY,
    lastX,
    lastY,
    lastCX,
    lastCY,
    lastScale,
    touchTime,
    touchLength,
    pause,
    reach,
  } = state;

  const sideBarOpen = useAppSelector((state) => state.globalState.sidebarOpen);
  const dynamicInnerWidth = sideBarOpen ? window.innerWidth - 300 : window.innerWidth;

  const fn = useMethods({
    onScale: (current: number) => onScale(limitScale(current)),
    onRotate(current: number) {
      if (rotate !== current) {
        expose({ rotate: current });
        updateState({
          rotate: current,
          ...getSuitableImageSize(naturalWidth, naturalHeight, current, dynamicInnerWidth),
        });
      }
    },
  });

  // zoom around the screen center by default
  function onScale(current: number, clientX?: number, clientY?: number) {
    if (scale !== current) {
      expose({ scale: current });
      updateState({
        scale: current,
        ...getPositionOnMoveOrScale(x, y, width, height, scale, current, clientX, clientY),
        ...(current <= 1 && { x: 0, y: 0 }),
      });
    }
  }

  const handleMove = useDebounceCallback(
    (nextClientX: number, nextClientY: number, currentTouchLength: number = 0) => {
      if ((touched || maskTouched) && isActive) {
        // swap width and height when rotated
        const [currentWidth, currentHeight] = getRotateSize(rotate, width, height);
        // with one finger at minimum zoom, use the initial move distance to detect intent
        if (currentTouchLength === 0 && initialTouchRef.current === 0) {
          const isStillX = Math.abs(nextClientX - CX) <= minStartTouchOffset;
          const isStillY = Math.abs(nextClientY - CY) <= minStartTouchOffset;
          // initial move distance is too small
          if (isStillX && isStillY) {
            // remember the last move distance for a smooth transition
            updateState({ lastCX: nextClientX, lastCY: nextClientY });
            return;
          }
          // set the response state
          initialTouchRef.current = !isStillX ? 1 : nextClientY > CY ? 3 : 2;
        }

        const offsetX = nextClientX - lastCX;
        const offsetY = nextClientY - lastCY;
        // edge trigger state
        let currentReach: ReachType;
        if (currentTouchLength === 0) {
          // edge overflow state
          const [horizontalCloseEdge] = computePositionEdge(offsetX + lastX, scale, currentWidth, dynamicInnerWidth);
          const [verticalCloseEdge] = computePositionEdge(offsetY + lastY, scale, currentHeight, innerHeight);
          // edge trigger detection
          currentReach = getReachType(initialTouchRef.current, horizontalCloseEdge, verticalCloseEdge, reach);

          // touching an edge
          if (currentReach !== undefined) {
            onReachMove(currentReach, nextClientX, nextClientY, scale);
          }
        }
        // disable the current swipe on horizontal edge or background trigger
        if (currentReach === "x" || maskTouched) {
          updateState({ reach: "x" });
          return;
        }
        // target scale
        const toScale = limitScale(
          scale + ((currentTouchLength - touchLength) / 100 / 2) * scale,
          naturalWidth / width,
          scaleBuffer,
        );
        // export variables
        expose({ scale: toScale });
        updateState({
          touchLength: currentTouchLength,
          reach: currentReach,
          scale: toScale,
          ...getPositionOnMoveOrScale(x, y, width, height, scale, toScale, nextClientX, nextClientY, offsetX, offsetY),
        });
      }
    },
    {
      maxWait: 8,
    },
  );

  function updateRaf(position: { x?: number; y?: number }) {
    if (stopRaf || touched) {
      return false;
    }
    if (mounted.current) {
      // allow animation when closing by pulling down
      updateState({ ...position, pause: visible });
    }
    return mounted.current;
  }

  const slideToPosition = useScrollPosition(
    (nextX) => updateRaf({ x: nextX }),
    (nextY) => updateRaf({ y: nextY }),
    (nextScale) => {
      if (mounted.current) {
        expose({ scale: nextScale });
        updateState({ scale: nextScale });
      }
      return !touched && mounted.current;
    },
    dynamicInnerWidth,
  );

  const handlePhotoTap = useContinuousTap(onPhotoTap, (currentClientX: number, currentClientY: number) => {
    if (!reach) {
      // if the image is large enough, scale it up to fit
      const endScale = scale !== 1 ? 1 : Math.max(2, naturalWidth / width);
      onScale(endScale, currentClientX, currentClientY);
    }
  });

  function handleUp(nextClientX: number, nextClientY: number) {
    // reset the response state
    initialTouchRef.current = 0;
    if ((touched || maskTouched) && isActive) {
      updateState({
        touched: false,
        maskTouched: false,
        pause: false,
        stopRaf: false,
        reach: undefined,
      });
      const safeScale = limitScale(scale, naturalWidth / width);
      // Go
      slideToPosition(x, y, lastX, lastY, width, height, scale, safeScale, lastScale, rotate, touchTime);

      onReachUp(nextClientX, nextClientY);
      // trigger the Tap event
      if (CX === nextClientX && CY === nextClientY) {
        if (touched) {
          handlePhotoTap(nextClientX, nextClientY);
          return;
        }
        if (maskTouched) {
          onMaskTap(nextClientX, nextClientY);
        }
      }
    }
  }

  useEventListener(isTouchDevice ? undefined : "mousemove", (e) => {
    handleMove(e.clientX, e.clientY);
  });
  useEventListener(isTouchDevice ? undefined : "mouseup", (e) => {
    handleUp(e.clientX, e.clientY);
  });
  useEventListener(
    isTouchDevice ? "touchmove" : undefined,
    (e) => {
      const position = getMultipleTouchPosition(e);
      handleMove(...position);
    },
    { passive: false },
  );
  useEventListener(
    isTouchDevice ? "touchend" : undefined,
    ({ changedTouches }) => {
      const touch = changedTouches[0];
      handleUp(touch.clientX, touch.clientY);
    },
    { passive: false },
  );
  useEventListener(
    "resize",
    useDebounceCallback(
      () => {
        if (loaded && !touched) {
          updateState(getSuitableImageSize(naturalWidth, naturalHeight, rotate, dynamicInnerWidth));
          onPhotoResize();
        }
      },
      { maxWait: 8 },
    ),
  );

  useIsomorphicLayoutEffect(() => {
    if (isActive) {
      expose({ scale, rotate, ...fn });
    }
  }, [isActive]);

  function handlePhotoLoad(params: IPhotoLoadedParams) {
    updateState({
      ...params,
      ...(params.loaded &&
        getSuitableImageSize(params.naturalWidth || 0, params.naturalHeight || 0, rotate, dynamicInnerWidth)),
    });
  }

  function handleStart(currentClientX: number, currentClientY: number, currentTouchLength: number = 0) {
    updateState({
      touched: true,
      CX: currentClientX,
      CY: currentClientY,
      lastCX: currentClientX,
      lastCY: currentClientY,
      lastX: x,
      lastY: y,
      lastScale: scale,
      touchLength: currentTouchLength,
      touchTime: Date.now(),
    });
  }

  function handleWheel(e: React.WheelEvent) {
    if (!reach) {
      // limit the maximum and minimum scale
      const toScale = limitScale(scale - e.deltaY / 100 / 2, naturalWidth / width);
      updateState({ stopRaf: true });
      onScale(toScale, e.clientX, e.clientY);
    }
  }

  function handleMaskStart(e: { clientX: number; clientY: number }) {
    updateState({
      maskTouched: true,
      CX: e.clientX,
      CY: e.clientY,
      lastX: x,
      lastY: y,
    });
  }

  function handleTouchStart(e: React.TouchEvent) {
    e.stopPropagation();
    handleStart(...getMultipleTouchPosition(e));
  }

  function handleMouseDown(e: React.MouseEvent) {
    e.stopPropagation();
    if (e.button === 0) {
      handleStart(e.clientX, e.clientY, 0);
    }
  }

  // calculate the position
  const [translateX, translateY, currentWidth, currentHeight, currentScale, opacity, easingMode, FIT] =
    useAnimationPosition(
      dynamicInnerWidth,
      visible,
      originRef,
      loaded,
      x,
      y,
      width,
      height,
      scale,
      speed,
      (isPause: boolean) => updateState({ pause: isPause }),
    );
  // image objectFit transition duration
  const transitionLayoutTime = easingMode < 4 ? speed / 2 : easingMode > 4 ? speed : 0;
  const transitionCSS = `transform ${speed}ms ${easing}`;

  const attrs = {
    className,
    onMouseDown: isTouchDevice ? undefined : handleMouseDown,
    onTouchStart: isTouchDevice ? handleTouchStart : undefined,
    onWheel: handleWheel,
    style: {
      width: currentWidth,
      height: currentHeight,
      opacity,
      objectFit: easingMode === 4 ? undefined : FIT,
      transform: rotate ? `rotate(${rotate}deg)` : undefined,
      transition:
        // no transition in the initial state
        easingMode > 2
          ? `${transitionCSS}, opacity ${speed}ms ease, height ${transitionLayoutTime}ms ${easing}`
          : undefined,
    },
  };

  return (
    <div
      className={`PhotoView__PhotoWrap${wrapClassName ? ` ${wrapClassName}` : ""}`}
      style={style}
      onMouseDown={!isTouchDevice && isActive ? handleMaskStart : undefined}
      onTouchStart={isTouchDevice && isActive ? (e) => handleMaskStart(e.touches[0]) : undefined}
    >
      <div
        className="PhotoView__PhotoBox"
        style={{
          transform: `matrix(${currentScale}, 0, 0, ${currentScale}, ${translateX}, ${translateY})`,
          transition: touched || pause ? undefined : transitionCSS,
          willChange: isActive ? "transform" : undefined,
        }}
      >
        {file ? (
          <Photo
            file={file}
            version={version}
            loaded={loaded}
            broken={broken}
            {...attrs}
            onPhotoLoad={handlePhotoLoad}
            loadingElement={loadingElement}
            brokenElement={brokenElement}
          />
        ) : (
          render && render({ attrs, scale: currentScale, rotate })
        )}
        {loadMorePlaceholder && <LoadMorePlaceholder key={key} />}
      </div>
    </div>
  );
}

const LoadMorePlaceholder = () => {
  const dispatch = useAppDispatch();
  const { ref, inView } = useInView({ triggerOnce: true });
  useEffect(() => {
    if (inView) {
      dispatch(loadMorePages(FileManagerIndex.main));
    }
  }, [inView]);
  return <FacebookCircularProgress ref={ref} fgColor={"#fff"} bgColor={grey[800]} />;
};
