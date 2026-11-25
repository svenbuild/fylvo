import type React from "react";
import { FileResponse } from "../../../../api/explorer.ts";

/**
 * Resource data type
 */
export interface DataType {
  /**
   * Unique key
   */
  key: number | string;
  /**
   * Resource URL
   */
  src?: string;
  /**
   * Custom render, lower priority than src
   */
  render?: (props: PhotoRenderParams) => React.ReactNode;
  /**
   * Custom overlay node
   */
  overlay?: React.ReactNode;
  /**
   * Width of the rendered node
   */
  width?: number;
  /**
   * Height of the rendered node
   */
  height?: number;
  /**
   * Trigger ref
   */
  originRef?: React.MutableRefObject<HTMLElement | null>;

  // Cloudreve specific
  file?: FileResponse;
  version?: string;

  loadMorePlaceholder?: boolean;
}

export interface PhotoProviderBase {
  /**
   * Whether to loop the preview, enabled once this count is reached
   * @defaultValue 3
   */
  loop?: boolean | number;
  /**
   * Animation speed
   * @defaultValue 400
   */
  speed?: (type: ActiveAnimationType) => number;
  /**
   * Animation easing function
   * @defaultValue 'cubic-bezier(0.25, 0.8, 0.25, 1)'
   */
  easing?: (type: ActiveAnimationType) => string;
  /**
   * Whether clicking the image closes the viewer
   */
  photoClosable?: boolean;
  /**
   * Whether clicking the background closes the viewer
   * @defaultValue true
   */
  maskClosable?: boolean;
  /**
   * Default background opacity
   * Set to null so the background ignores pull-down changes
   * @defaultValue 1
   */
  maskOpacity?: number | null;
  /**
   * Whether pulling down closes the viewer
   * @defaultValue true
   */
  pullClosable?: boolean;
  /**
   * Navigation bar visible
   * @defaultValue true
   */
  bannerVisible?: boolean;
  /**
   * Custom overlay render
   */
  overlayRender?: (overlayProps: OverlayRenderProps) => React.ReactNode;
  /**
   * Custom toolbar render
   */
  toolbarRender?: (overlayProps: OverlayRenderProps) => React.ReactNode;
  className?: string;
  maskClassName?: string;
  photoWrapClassName?: string;
  photoClassName?: string;
  /**
   * Custom loading
   */
  loadingElement?: JSX.Element;
  /**
   * Custom render for load failure
   */
  brokenElement?: JSX.Element | ((photoProps: BrokenElementParams) => JSX.Element);
  /**
   * @defaultValue document.body
   */
  portalContainer?: HTMLElement;
}

export type PhotoRenderParams = {
  /**
   * Custom render DOM attributes
   */
  attrs: Partial<React.HTMLAttributes<HTMLElement>>;
  scale: number;
  rotate: number;
};

/**
 * brokenElement function parameters
 */
export interface BrokenElementParams {
  src: string;
}

export interface OverlayRenderProps {
  /**
   * Image list
   */
  images: DataType[];
  /**
   * Current index
   */
  index: number;
  /**
   * Index change callback
   */
  onIndexChange: (index: number) => void;
  /**
   * Whether visible
   */
  visible: boolean;
  /**
   * Close callback
   */
  onClose: (evt?: React.MouseEvent | React.TouchEvent) => void;
  /**
   * Whether the overlay is visible
   */
  overlayVisible: boolean;
  /**
   * Custom overlay node
   */
  overlay?: React.ReactNode;
  /**
   * Current rotation angle
   */
  rotate: number;
  /**
   * Rotation callback
   */
  onRotate: (rotate: number) => void;
  /**
   * Current scale
   */
  scale: number;
  /**
   * Scale callback
   */
  onScale: (scale: number) => void;
}

export interface ExposedProperties {
  // Scale
  scale?: number;
  // Rotation
  rotate?: number;
  // Scale callback
  onScale?: (scale: number) => void;
  // Rotation callback
  onRotate?: (rotate: number) => void;
}

export type ReachMoveFunction = (reachPosition: ReachType, clientX: number, clientY: number, scale?: number) => void;

export type ReachFunction = (clientX: number, clientY: number) => void;

export type PhotoTapFunction = (clientX: number, clientY: number) => void;

/**
 * Edge overflow state
 */
export type CloseEdgeType =
  | 1 // Smaller than the screen width
  | 2 // Touching the left/top edge
  | 3 // Touching the right/bottom edge
  | undefined; // Normal sliding

/**
 * Edge trigger state
 */
export type ReachType =
  | "x" // x axis
  | "y" // y axis
  | undefined; // Not triggered

/**
 * Initial response state
 */
export type TouchStartType =
  | 0 // Not triggered
  | 1 // X axis first
  | 2 // Y axis up, push
  | 3; // Y axis down, pull

export type OriginRectType = {
  // top
  T: number;
  // left
  L: number;
  // width
  W: number;
  // height
  H: number;
  // object-fit
  FIT: "contain" | "cover" | "fill" | undefined;
};

/**
 * Animation state
 */
export type EasingMode =
  // Not initialized
  | 0
  // Enter: start
  | 1
  // Enter: animation start
  | 2
  // Enter: second animation frame
  | 3
  // Normal
  | 4
  // Closed
  | 5;

/**
 * Running animation
 */
export type ActiveAnimationType =
  // Not initialized
  | 0
  // Enter
  | 1
  // Leave
  | 2
  // Switch
  | 3;
