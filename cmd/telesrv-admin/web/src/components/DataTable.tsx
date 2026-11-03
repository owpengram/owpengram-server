import { ArrowDown, ArrowUp, ArrowUpArrowDown, Xmark } from "@gravity-ui/icons";
import { useMemo, useState, type ReactNode } from "react";

// DataTable is the one list every page uses: click a header to sort, click a
// row to open it. Sorting is local to the rows it is given unless the page
// passes `sort`/`onSort`, in which case the page owns the order (for lists the
// server sorts, like Storage's per-account usage).

export type SortDir = "asc" | "desc";
export type SortState = { key: string; dir: SortDir };

export type Column<T> = {
  key: string;
  label: string;
  render: (row: T) => ReactNode;
  // Present = the column is sortable. Strings sort case-insensitively.
  sortValue?: (row: T) => string | number | bigint | null | undefined;
  align?: "left" | "right";
  width?: string;
  className?: string;
  // The direction a first click sorts in; numbers and dates usually want desc.
  firstDir?: SortDir;
};

function compare(a: unknown, b: unknown): number {
  if (a === b) return 0;
  if (a === null || a === undefined || a === "") return 1;
  if (b === null || b === undefined || b === "") return -1;
  if (typeof a === "string" && typeof b === "string") {
    return a.localeCompare(b, undefined, { sensitivity: "base", numeric: true });
  }
  return (a as number) < (b as number) ? -1 : 1;
}

export function DataTable<T>({
  columns,
  rows,
  rowKey,
  onRowClick,
  defaultSort,
  sort: controlledSort,
  onSort,
  loading = false,
  empty = "Nothing here yet."
}: {
  columns: Column<T>[];
  rows: T[];
  rowKey: (row: T) => string | number;
  onRowClick?: (row: T) => void;
  defaultSort?: SortState;
  sort?: SortState;
  onSort?: (next: SortState) => void;
  loading?: boolean;
  empty?: ReactNode;
}) {
  const [localSort, setLocalSort] = useState<SortState | undefined>(defaultSort);
  const sort = controlledSort ?? localSort;

  function toggle(column: Column<T>) {
    const next: SortState =
      sort?.key === column.key
        ? { key: column.key, dir: sort.dir === "asc" ? "desc" : "asc" }
        : { key: column.key, dir: column.firstDir ?? "asc" };
    if (onSort) onSort(next);
    else setLocalSort(next);
  }

  const sorted = useMemo(() => {
    // A page that owns the order hands rows over already sorted.
    if (controlledSort || !sort) return rows;
    const column = columns.find((c) => c.key === sort.key);
    if (!column?.sortValue) return rows;
    const value = column.sortValue;
    const out = [...rows].sort((a, b) => compare(value(a), value(b)));
    return sort.dir === "desc" ? out.reverse() : out;
  }, [rows, columns, sort, controlledSort]);

  return (
    <div className="table-wrap">
      <table className="data-table">
        <thead>
          <tr>
            {columns.map((column) => {
              const active = sort?.key === column.key;
              // className styles the cells only; headers stay in the header font.
              const classes = column.align === "right" ? "num" : "";
              return (
                <th
                  key={column.key}
                  className={classes || undefined}
                  style={column.width ? { width: column.width } : undefined}
                  aria-sort={active ? (sort?.dir === "asc" ? "ascending" : "descending") : undefined}
                >
                  {column.sortValue || onSort ? (
                    <button type="button" className={`sort-header ${active ? "active" : ""}`.trim()} onClick={() => toggle(column)}>
                      {column.label}
                      {active ? (
                        sort?.dir === "asc" ? <ArrowUp width={12} height={12} /> : <ArrowDown width={12} height={12} />
                      ) : (
                        <ArrowUpArrowDown width={12} height={12} className="sort-header-idle" />
                      )}
                    </button>
                  ) : (
                    column.label
                  )}
                </th>
              );
            })}
          </tr>
        </thead>
        <tbody>
          {sorted.map((row) => (
            <tr
              key={rowKey(row)}
              className={onRowClick ? "row-clickable" : undefined}
              onClick={onRowClick ? (event) => {
                // Buttons and links inside a row keep their own job.
                if ((event.target as HTMLElement).closest("button, a, input, select, label")) return;
                onRowClick(row);
              } : undefined}
            >
              {columns.map((column) => (
                <td key={column.key} className={[column.align === "right" ? "num" : "", column.className ?? ""].join(" ").trim() || undefined}>
                  {column.render(row)}
                </td>
              ))}
            </tr>
          ))}
          {sorted.length === 0 && loading &&
            [0, 1, 2].map((i) => (
              <tr key={`loading-${i}`} className="loading-row" aria-hidden="true">
                {columns.map((column) => (
                  <td key={column.key}><span className="skeleton skeleton-text" /></td>
                ))}
              </tr>
            ))}
          {sorted.length === 0 && !loading && (
            <tr>
              <td className="empty-cell" colSpan={columns.length}>{empty}</td>
            </tr>
          )}
        </tbody>
      </table>
    </div>
  );
}

// FilterChip is one on/off filter next to the search box. Active chips carry a
// small cross so the way back out is where the eye already is.
export function FilterChip({
  label,
  active,
  onToggle,
  count
}: {
  label: string;
  active: boolean;
  onToggle: () => void;
  count?: number;
}) {
  return (
    <button type="button" className={`filter-chip ${active ? "active" : ""}`.trim()} aria-pressed={active} onClick={onToggle}>
      {label}
      {count !== undefined && <span className="filter-chip-count">{count}</span>}
      {active && <Xmark width={12} height={12} />}
    </button>
  );
}
