import { useCallback, useEffect, useRef, useState } from "react";
import { ActivityEntry, ListActivityResponse } from "../../../api/activity.ts";

// useActivityFeed loads entries page by page (Fylvo). load gets the cursor of the next page and
// starts over whenever key changes.
export const useActivityFeed = (key: string | undefined, load: (next?: number) => Promise<ListActivityResponse>) => {
  const [entries, setEntries] = useState<ActivityEntry[]>([]);
  const [next, setNext] = useState<number | undefined>(undefined);
  const [loading, setLoading] = useState(false);
  const loadRef = useRef(load);
  loadRef.current = load;
  // Answers for an older key are dropped.
  const generation = useRef(0);

  const fetchPage = useCallback((cursor?: number) => {
    const current = generation.current;
    setLoading(true);
    loadRef
      .current(cursor)
      .then((res) => {
        if (current !== generation.current) {
          return;
        }
        setEntries((old) => (cursor ? [...old, ...res.entries] : res.entries));
        setNext(res.next || undefined);
      })
      .catch(() => {})
      .finally(() => {
        if (current === generation.current) {
          setLoading(false);
        }
      });
  }, []);

  useEffect(() => {
    generation.current++;
    setEntries([]);
    setNext(undefined);
    if (key !== undefined) {
      fetchPage();
    }
  }, [key, fetchPage]);

  return {
    entries,
    loading,
    hasMore: !!next,
    loadMore: () => fetchPage(next),
  };
};
