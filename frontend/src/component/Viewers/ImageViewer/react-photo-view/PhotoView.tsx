import type React from "react";
import { useImperativeHandle, Children, cloneElement, useContext, useEffect, useMemo, useRef } from "react";
import useInitial from "./hooks/useInitial";
import useMethods from "./hooks/useMethods";
import type { PhotoContextType } from "./photo-context";
import PhotoContext from "./photo-context";
import type { PhotoRenderParams } from "./types";

export interface PhotoViewProps {
  /**
   * Image URL
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
   * Width of the custom rendered node
   */
  width?: number;
  /**
   * Height of the custom rendered node
   */
  height?: number;
  /**
   * Child node, usually a thumbnail
   */
  children?: React.ReactElement;
  /**
   * Trigger event
   */
  triggers?: ("onClick" | "onDoubleClick")[];
}

const PhotoView: React.FC<PhotoViewProps> = ({
  src,
  render,
  overlay,
  width,
  height,
  triggers = ["onClick"],
  children,
}) => {
  const photoContext = useContext<PhotoContextType>(PhotoContext);
  const key = useInitial(() => photoContext.nextId());
  const originRef = useRef<HTMLElement>(null);

  useImperativeHandle((children as React.FunctionComponentElement<HTMLElement>)?.ref, () => originRef.current);

  useEffect(() => {
    return () => {
      photoContext.remove(key);
    };
  }, []);

  function invokeChildrenFn(eventName: string, e: React.SyntheticEvent) {
    if (children) {
      const eventFn = children.props[eventName];
      if (eventFn) {
        eventFn(e);
      }
    }
  }

  const fn = useMethods({
    render(props: PhotoRenderParams) {
      return render && render(props);
    },
    show(eventName: string, e: React.MouseEvent) {
      photoContext.show(key);
      invokeChildrenFn(eventName, e);
    },
  });

  const eventListeners = useMemo(() => {
    const listener = {};
    triggers.forEach((eventName) => {
      listener[eventName] = fn.show.bind(null, eventName);
    });
    return listener;
  }, []);

  useEffect(() => {
    photoContext.update({
      key,
      src,
      originRef,
      render: fn.render,
      overlay,
      width,
      height,
    });
  }, [src]);

  if (children) {
    return Children.only(cloneElement(children, { ...eventListeners, ref: originRef }));
  }
  return null;
};

export default PhotoView;
