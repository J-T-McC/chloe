// Shape tiles (Navicat's dashboard shapes): lines, arrows and outlines drawn to
// the tile's size, and icons for diagrams (database, cloud, server…).
import { Cloud, Database, Monitor, Server, Smartphone, User } from "lucide-react";
import type { B } from "../../api";
import { ChartTheme, TOKENS } from "./theme";

export const SHAPES: [string, string][] = [
  ["line", "Line"],
  ["arrow", "Arrow"],
  ["rectangle", "Rectangle"],
  ["ellipse", "Ellipse"],
  ["triangle", "Triangle"],
  ["right_triangle", "Right Triangle"],
  ["right_arrow", "Right Arrow"],
  ["database", "Database"],
  ["cloud", "Cloud"],
  ["server", "Server"],
  ["desktop", "Desktop"],
  ["mobile", "Mobile"],
  ["person", "Person"],
];

const ICONS = { database: Database, cloud: Cloud, server: Server, desktop: Monitor, mobile: Smartphone, person: User } as const;

export function ShapeTile({ item, theme }: { item: B.Item; theme: ChartTheme }) {
  const color = item.color || TOKENS[theme].ink2;
  const w = Math.max(1, item.lineWidth || 2);
  const dash = item.dashed ? `${w * 3} ${w * 2}` : undefined;
  const shape = item.shape ?? "rectangle";
  const Icon = ICONS[shape as keyof typeof ICONS];
  if (Icon)
    return (
      <div className="bi-shape" data-testid="bi-shape" data-shape={shape}>
        <Icon color={color} strokeWidth={Math.min(w, 3)} fill={item.filled ? color : "none"} fillOpacity={item.filled ? 0.18 : 0} style={{ width: "100%", height: "100%" }} />
      </div>
    );
  const fill = item.filled ? color : "none";
  const stroke = { stroke: color, strokeWidth: w, strokeDasharray: dash, vectorEffect: "non-scaling-stroke" as const, strokeLinejoin: "round" as const, strokeLinecap: "round" as const };
  // Drawn in a 100×100 box stretched to the tile; strokes keep their width.
  let body: React.ReactNode;
  switch (shape) {
    case "line":
      body = <line x1={2} y1={50} x2={98} y2={50} {...stroke} />;
      break;
    case "arrow":
      body = (
        <>
          <line x1={2} y1={50} x2={96} y2={50} {...stroke} />
          <polyline points="86,38 96,50 86,62" fill="none" {...stroke} />
        </>
      );
      break;
    case "ellipse":
      body = <ellipse cx={50} cy={50} rx={48} ry={48} fill={fill} fillOpacity={0.18} {...stroke} />;
      break;
    case "triangle":
      body = <polygon points="50,2 98,98 2,98" fill={fill} fillOpacity={0.18} {...stroke} />;
      break;
    case "right_triangle":
      body = <polygon points="2,2 2,98 98,98" fill={fill} fillOpacity={0.18} {...stroke} />;
      break;
    case "right_arrow":
      body = <polygon points="2,30 62,30 62,6 98,50 62,94 62,70 2,70" fill={fill} fillOpacity={0.18} {...stroke} />;
      break;
    default:
      body = <rect x={2} y={2} width={96} height={96} rx={3} fill={fill} fillOpacity={0.18} {...stroke} />;
  }
  return (
    <div className="bi-shape" data-testid="bi-shape" data-shape={shape}>
      <svg viewBox="0 0 100 100" preserveAspectRatio="none" width="100%" height="100%" aria-hidden>
        {body}
      </svg>
    </div>
  );
}
