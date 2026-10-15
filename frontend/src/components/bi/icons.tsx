import { ChartArea, ChartBar, ChartBarStacked, ChartColumn, ChartColumnStacked, ChartLine, ChartNoAxesCombined, ChartPie, Donut, Hash, LucideProps, Table2 } from "lucide-react";

const ICONS: Record<string, React.ComponentType<LucideProps>> = {
  bar: ChartColumn,
  stacked_bar: ChartColumnStacked,
  hbar: ChartBar,
  stacked_hbar: ChartBarStacked,
  line: ChartLine,
  area: ChartArea,
  stacked_area: ChartArea,
  bar_line: ChartNoAxesCombined,
  stacked_bar_line: ChartNoAxesCombined,
  pie: ChartPie,
  donut: Donut,
  value: Hash,
  table: Table2,
};

export function ChartTypeIcon({ type, ...p }: { type: string } & LucideProps) {
  const I = ICONS[type] ?? ChartColumn;
  return <I {...p} />;
}
