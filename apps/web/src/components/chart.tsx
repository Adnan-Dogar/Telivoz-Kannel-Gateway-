import * as React from "react";
import ReactECharts from "echarts-for-react";
import type { EChartsOption } from "echarts";

function cssVar(name: string) {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
}

// Resolves theme tokens so charts follow light/dark mode.
export function useChartTheme() {
  const [tick, setTick] = React.useState(0);
  React.useEffect(() => {
    const h = () => setTick((t) => t + 1);
    window.addEventListener("themechange", h);
    return () => window.removeEventListener("themechange", h);
  }, []);
  return React.useMemo(
    () => ({
      text: cssVar("--muted-foreground"),
      fg: cssVar("--foreground"),
      grid: cssVar("--border"),
      card: cssVar("--card"),
      colors: ["--chart-1", "--chart-2", "--chart-3", "--chart-4", "--chart-5"].map(cssVar),
      success: cssVar("--success"),
      danger: cssVar("--danger"),
      warning: cssVar("--warning"),
      primary: cssVar("--primary"),
      tick,
    }),
    [tick],
  );
}

export type ChartTheme = ReturnType<typeof useChartTheme>;

export function baseOption(t: ChartTheme): EChartsOption {
  return {
    color: t.colors,
    textStyle: { fontFamily: "inherit", color: t.text },
    grid: { left: 8, right: 12, top: 28, bottom: 8, containLabel: true },
    tooltip: {
      trigger: "axis",
      backgroundColor: t.card,
      borderColor: t.grid,
      textStyle: { color: t.fg, fontSize: 12 },
      axisPointer: { type: "line", lineStyle: { color: t.grid } },
    },
    legend: { top: 0, right: 0, icon: "roundRect", itemWidth: 10, itemHeight: 10, textStyle: { color: t.text } },
  };
}

export function Chart({ option, height = 280 }: { option: EChartsOption; height?: number }) {
  return <ReactECharts option={option} notMerge lazyUpdate style={{ height, width: "100%" }} opts={{ renderer: "svg" }} />;
}
