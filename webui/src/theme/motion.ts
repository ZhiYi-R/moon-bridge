import type { Transition, Variants } from "motion/react";

/**
 * Bauhaus motion: short, decisive, no springy overshoot.
 * API surface matches the previous motion helpers so call sites stay stable.
 */
export const springs = {
  spatialFast: { type: "tween", duration: 0.1, ease: [0.25, 0.1, 0.25, 1] },
  spatial: { type: "tween", duration: 0.16, ease: [0.25, 0.1, 0.25, 1] },
  spatialSlow: { type: "tween", duration: 0.24, ease: [0, 0, 0.2, 1] },
  effectsFast: { type: "tween", duration: 0.1, ease: "linear" },
  effects: { type: "tween", duration: 0.14, ease: "linear" },
  effectsSlow: { type: "tween", duration: 0.2, ease: "linear" }
} as const satisfies Record<string, Transition>;

export const easing = {
  standard: "cubic-bezier(0.25, 0.1, 0.25, 1)",
  standardDecelerate: "cubic-bezier(0, 0, 0.2, 1)",
  standardAccelerate: "cubic-bezier(0.4, 0, 1, 1)",
  emphasized: "cubic-bezier(0.25, 0.1, 0.25, 1)",
  emphasizedDecelerate: "cubic-bezier(0, 0, 0.2, 1)",
  emphasizedAccelerate: "cubic-bezier(0.4, 0, 1, 1)"
} as const;

export const pageMotion = {
  initial: { opacity: 0, y: 6 },
  animate: { opacity: 1, y: 0 },
  transition: { ...springs.spatial, opacity: springs.effects }
} as const;

export const surfaceMotion = {
  initial: { opacity: 0, y: 8 },
  animate: { opacity: 1, y: 0 },
  transition: { ...springs.spatial, opacity: springs.effects }
} as const;

export const listContainer: Variants = {
  hidden: {},
  show: {
    transition: { staggerChildren: 0.03, delayChildren: 0.01 }
  }
};

export const listItem: Variants = {
  hidden: { opacity: 0, y: 6 },
  show: {
    opacity: 1,
    y: 0,
    transition: { ...springs.spatial, opacity: springs.effects }
  }
};

export const pressable = {
  whileHover: { scale: 1 },
  whileTap: { scale: 0.98 },
  transition: springs.spatialFast
} as const;

export const collapse: Variants = {
  hidden: { opacity: 0, height: 0, y: -2 },
  show: {
    opacity: 1,
    height: "auto",
    y: 0,
    transition: { ...springs.spatial, opacity: springs.effectsFast }
  }
};
