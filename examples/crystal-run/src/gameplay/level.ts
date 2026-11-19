export const platforms = [
  { x: 0, z: 0, w: 10, d: 10 },
  { x: 0, z: -12, w: 7, d: 10 },
  { x: 5, z: -23, w: 8, d: 9 },
  { x: -2, z: -34, w: 9, d: 10 },
  { x: 3, z: -46, w: 10, d: 10 },
];
export const crystals = [
  { id: "dawn", x: -3, z: -2 },
  { id: "tide", x: 1, z: -12 },
  { id: "sky", x: 6, z: -23 },
  { id: "star", x: -4, z: -35 },
  { id: "moon", x: 5, z: -47 },
];
export const portal = { x: 3, z: -49 };
export function hazardAt(time: number) {
  return { x: 5 + Math.sin(time * 1.7) * 3, z: -24 };
}
