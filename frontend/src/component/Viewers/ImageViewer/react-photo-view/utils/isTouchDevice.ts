/**
 * Whether touch devices are supported
 */
const isTouchDevice = typeof window !== "undefined" && "ontouchstart" in window;

export default isTouchDevice;
