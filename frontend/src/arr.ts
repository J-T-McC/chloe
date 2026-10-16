/** Go nil slices arrive as null; normalise to an empty array. */
export const arr = <T,>(x: T[] | null | undefined): T[] => x ?? [];
