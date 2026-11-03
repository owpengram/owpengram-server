import { useEffect, useRef, useState, type ReactNode } from "react";

// Small hand-drawn SVG charts for the overview. No chart library: the panel
// needs a sparkline and one bar chart, and both stay crisp at any width
// because they are drawn in real pixels from the measured box.

function useWidth<T extends HTMLElement>(): [React.RefObject<T>, number] {
  const ref = useRef<T>(null);
  const [width, setWidth] = useState(0);
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const observer = new ResizeObserver(([entry]) => setWidth(Math.floor(entry.contentRect.width)));
    observer.observe(el);
    setWidth(Math.floor(el.getBoundingClientRect().width));
    return () => observer.disconnect();
  }, []);
  return [ref, width];
}

// Sparkline: a filled line for a short series. `max` pins the top of the
// scale (100 for a percentage); otherwise the series' own peak is the top.
export function Sparkline({
  values,
  height = 44,
  max,
  tone = "brand",
  label
}: {
  values: number[];
  height?: number;
  max?: number;
  tone?: "brand" | "good" | "warn";
  label: string;
}) {
  const [ref, width] = useWidth<HTMLDivElement>();
  const top = Math.max(max ?? 0, ...values, 1);
  const points = values.map((v, i) => {
    const x = values.length > 1 ? (i / (values.length - 1)) * width : width;
    const y = height - 2 - (Math.max(0, v) / top) * (height - 6);
    return [x, y] as const;
  });
  const line = points.map(([x, y], i) => `${i === 0 ? "M" : "L"}${x.toFixed(1)} ${y.toFixed(1)}`).join("");
  const area = points.length > 1 ? `${line}L${width} ${height}L0 ${height}Z` : "";
  return (
    <div ref={ref} className={`sparkline sparkline--${tone}`} style={{ height }} role="img" aria-label={label}>
      {width > 0 && points.length > 1 && (
        <svg width={width} height={height} viewBox={`0 0 ${width} ${height}`}>
          <path d={area} className="sparkline-area" />
          <path d={line} className="sparkline-line" />
        </svg>
      )}
    </div>
  );
}

export type BarDatum = {
  key: string;
  label: string;
  bar: number;
  line?: number;
};

// BarLineChart: one bar per day with an optional line over it, each on its
// own scale (messages and sign-ups differ by orders of magnitude). Hovering a
// day shows its numbers.
export function BarLineChart({
  data,
  height = 220,
  barName,
  lineName,
  tooltip
}: {
  data: BarDatum[];
  height?: number;
  barName: string;
  lineName?: string;
  tooltip: (d: BarDatum) => ReactNode;
}) {
  const [ref, width] = useWidth<HTMLDivElement>();
  const [hover, setHover] = useState<number | null>(null);
  const padTop = 12;
  const padBottom = 24;
  const plot = height - padTop - padBottom;
  const barMax = Math.max(1, ...data.map((d) => d.bar));
  const lineMax = Math.max(1, ...data.map((d) => d.line ?? 0));
  const step = data.length > 0 ? width / data.length : 0;
  const barW = Math.max(2, Math.min(28, step * 0.62));
  const y = (v: number, m: number) => padTop + plot - (v / m) * plot;
  const linePath = lineName
    ? data.map((d, i) => `${i === 0 ? "M" : "L"}${(i * step + step / 2).toFixed(1)} ${y(d.line ?? 0, lineMax).toFixed(1)}`).join("")
    : "";
  // Label every few days so the axis never crowds.
  const labelEvery = Math.max(1, Math.ceil(data.length / Math.max(1, Math.floor(width / 56))));

  return (
    <div className="bar-chart" ref={ref} onMouseLeave={() => setHover(null)}>
      <div className="bar-chart-legend">
        <span className="legend-bar">{barName}</span>
        {lineName && <span className="legend-line">{lineName}</span>}
      </div>
      {width > 0 && (
        <svg width={width} height={height} viewBox={`0 0 ${width} ${height}`} role="img" aria-label={`${barName}${lineName ? ` and ${lineName}` : ""} per day`}>
          {[0.25, 0.5, 0.75, 1].map((f) => (
            <line key={f} x1={0} x2={width} y1={padTop + plot - f * plot} y2={padTop + plot - f * plot} className="bar-chart-grid" />
          ))}
          {data.map((d, i) => {
            const h = (d.bar / barMax) * plot;
            return (
              <g key={d.key} onMouseEnter={() => setHover(i)}>
                {/* Full-height hit area, so thin or empty days are easy to hover. */}
                <rect x={i * step} y={padTop} width={step} height={plot} fill="transparent" />
                <rect
                  x={i * step + (step - barW) / 2}
                  y={padTop + plot - Math.max(h, d.bar > 0 ? 2 : 0)}
                  width={barW}
                  height={Math.max(h, d.bar > 0 ? 2 : 0)}
                  rx={Math.min(4, barW / 3)}
                  className={`bar-chart-bar${hover === i ? " active" : ""}`}
                />
                {i % labelEvery === 0 && (
                  <text x={i * step + step / 2} y={height - 6} className="bar-chart-axis" textAnchor="middle">
                    {d.label}
                  </text>
                )}
              </g>
            );
          })}
          {lineName && <path d={linePath} className="bar-chart-line" />}
          {lineName &&
            data.map((d, i) =>
              hover === i ? (
                <circle key={d.key} cx={i * step + step / 2} cy={y(d.line ?? 0, lineMax)} r={4} className="bar-chart-dot" />
              ) : null
            )}
        </svg>
      )}
      {hover !== null && data[hover] && width > 0 && (
        <div
          className="chart-tooltip"
          style={{ left: Math.min(Math.max(hover * step + step / 2, 70), width - 70), top: 22 }}
        >
          {tooltip(data[hover])}
        </div>
      )}
    </div>
  );
}

// Meter: a thin horizontal fill for "how full is this".
export function Meter({ percent, warnAbove = 75, dangerAbove = 90 }: { percent: number; warnAbove?: number; dangerAbove?: number }) {
  const p = Math.max(0, Math.min(100, percent));
  const tone = p >= dangerAbove ? "danger" : p >= warnAbove ? "warn" : "ok";
  return (
    <div className={`meter meter--${tone}`} role="meter" aria-valuenow={Math.round(p)} aria-valuemin={0} aria-valuemax={100}>
      <span style={{ width: `${p}%` }} />
    </div>
  );
}
