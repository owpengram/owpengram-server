import { useEffect, useState } from "react";

// useDebounced returns value once it has stopped changing for `ms`, so a
// search box can query as the operator types without a request per keystroke.
export function useDebounced<T>(value: T, ms = 300): T {
  const [settled, setSettled] = useState(value);
  useEffect(() => {
    const timer = window.setTimeout(() => setSettled(value), ms);
    return () => window.clearTimeout(timer);
  }, [value, ms]);
  return settled;
}
