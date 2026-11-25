import { useRef } from "react";
import useDebounceCallback from "./useDebounceCallback";

export type TapFuncType<T> = (...args: T[]) => void;

/**
 * handles single and double click events
 * @param singleTap - single click event
 * @param doubleTap - double click event
 * @return invokeTap
 */
export default function useContinuousTap<T>(singleTap: TapFuncType<T>, doubleTap: TapFuncType<T>): TapFuncType<T> {
  // current number of consecutive taps
  const continuousClick = useRef(0);

  const debounceTap = useDebounceCallback(
    (...args) => {
      continuousClick.current = 0;
      singleTap(...args);
    },
    {
      wait: 300,
    },
  );

  return function invokeTap(...args) {
    continuousClick.current += 1;
    debounceTap(...args);
    // double tap
    if (continuousClick.current >= 2) {
      debounceTap.cancel();
      continuousClick.current = 0;
      doubleTap(...args);
    }
  };
}
