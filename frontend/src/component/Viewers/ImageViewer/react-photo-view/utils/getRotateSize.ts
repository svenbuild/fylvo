/**
 * get the width and height after rotation
 */
export default function getRotateSize(rotate: number, width: number, height: number) {
  const isVertical = rotate % 180 !== 0;

  // swap the properties if the image is not horizontal
  if (isVertical) {
    return [height, width, isVertical] as const;
  }

  return [width, height, isVertical] as const;
}
