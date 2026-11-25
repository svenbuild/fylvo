import { useReducer, useRef } from "react";
import type { ActiveAnimationType } from "../types";
import useForkedVariable from "./useForkedVariable";

/**
 * handles the real closed state after the close animation
 * implements leaveCallback via the onAnimationEnd callback
 */
export default function useAnimationVisible(
  visible: boolean | undefined,
  afterClose?: () => void,
): [realVisible: boolean | undefined, activeAnimation: ActiveAnimationType, onAnimationEnd: () => void] {
  const [, handleRender] = useReducer((c) => !c, false);

  const activeAnimation = useRef<ActiveAnimationType>(0);

  // visible state branch
  const [realVisible, modifyRealVisible] = useForkedVariable(visible, (modify) => {
    // visible: set the enter animation
    if (visible) {
      modify(visible);
      activeAnimation.current = 1;
    } else {
      activeAnimation.current = 2;
    }
  });

  function onAnimationEnd() {
    // trigger a render after the animation ends
    handleRender();
    // end animation: set the hidden state
    if (activeAnimation.current === 2) {
      modifyRealVisible(false);
      // trigger the hide callback
      if (afterClose) {
        afterClose();
      }
    }
    // reset state
    activeAnimation.current = 0;
  }

  return [
    /**
     * real visible state
     */
    realVisible,
    /**
     * animation in progress
     */
    activeAnimation.current,
    /**
     * callback after the animation ends
     */
    onAnimationEnd,
  ];
}
