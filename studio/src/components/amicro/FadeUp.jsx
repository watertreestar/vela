// Adapted from Amicro's MIT-licensed Fade Up registry primitive:
// https://github.com/Subhan-code/Amicro--Micro-transitions-/blob/main/registry/ui/entrance/fade-up.tsx
import { motion } from "framer-motion";

export function FadeUp({ children, className = "" }) {
  const reducedMotion = typeof window !== "undefined" && window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;
  return <motion.div
    className={className}
    initial={reducedMotion ? false : { opacity: 0, y: 8 }}
    animate={{ opacity: 1, y: 0 }}
    transition={reducedMotion ? { duration: 0 } : { duration: 0.24, ease: [0.16, 1, 0.3, 1] }}
  >{children}</motion.div>;
}
