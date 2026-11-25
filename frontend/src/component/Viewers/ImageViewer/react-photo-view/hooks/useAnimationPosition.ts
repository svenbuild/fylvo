import type { MutableRefObject } from "react";
import useAnimationOrigin from "./useAnimationOrigin";
import useTargetScale from "./useTargetScale";

export default function useAnimationPosition(
  dynamicInnerWidth: number,
  visible: boolean | undefined,
  originRef: MutableRefObject<HTMLElement | null> | undefined,
  loaded: boolean,
  x: number,
  y: number,
  width: number,
  height: number,
  scale: number,
  speed: number,
  updateEasing: (pause: boolean) => void,
) {
  // delay updating width/height
  const [autoWidth, autoHeight, autoScale] = useTargetScale(width, height, scale, speed, updateEasing);
  // animation source handling
  const [easingMode, originRect] = useAnimationOrigin(visible, originRef, loaded, speed, updateEasing);

  // compute the animation position
  const { T, L, W, H, FIT } = originRect;
  // offset, x: 0, y: 0 (centered) is the initial value
  const centerWidth = dynamicInnerWidth / 2;
  const centerHeight = innerHeight / 2;
  const offsetX = centerWidth - (width * scale) / 2;
  const offsetY = centerHeight - (height * scale) / 2;
  // thumbnail state
  const miniMode = easingMode < 3 || easingMode > 4;
  // with a thumbnail, use the thumbnail position, otherwise center
  const translateX = miniMode ? (W ? L : centerWidth) : x + offsetX;
  const translateY = miniMode ? (W ? T : centerHeight) : y + offsetY;

  // minimum scale
  const minScale = W / (width * scale) || 0.01;

  // follow objectFit to keep the thumbnail aspect ratio
  const currentHeight = miniMode && FIT ? autoWidth * (H / W) : autoHeight;
  // no scaling on initial load
  const currentScale = easingMode === 0 ? autoScale : miniMode ? minScale : autoScale;
  const opacity = miniMode ? (FIT ? 1 : 0) : 1;

  return [translateX, translateY, autoWidth, currentHeight, currentScale, opacity, easingMode, FIT] as const;
}
