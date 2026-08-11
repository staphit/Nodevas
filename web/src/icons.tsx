// Inline SVG icon set — consistent 1.75 stroke, currentColor, no external
// icon library (taste-skill: no generic thin-line kits, standard weight).
import type { SVGProps } from "react";

type P = SVGProps<SVGSVGElement> & { size?: number };

function base({ size = 16, ...rest }: P) {
  return {
    width: size,
    height: size,
    viewBox: "0 0 24 24",
    fill: "none",
    stroke: "currentColor",
    strokeWidth: 1.75,
    strokeLinecap: "round" as const,
    strokeLinejoin: "round" as const,
    "aria-hidden": true,
    ...rest,
  };
}

export const IconSun = (p: P) => (
  <svg {...base(p)}>
    <circle cx="12" cy="12" r="4.5" />
    <path d="M12 2.5v2.5M12 19v2.5M21.5 12H19M5 12H2.5M18.7 5.3l-1.8 1.8M7.1 16.9l-1.8 1.8M18.7 18.7l-1.8-1.8M7.1 7.1 5.3 5.3" />
  </svg>
);

export const IconMoon = (p: P) => (
  <svg {...base(p)}>
    <path d="M20.5 14.5A8.5 8.5 0 0 1 9.5 3.5a8.5 8.5 0 1 0 11 11Z" />
  </svg>
);

export const IconCloud = (p: P) => (
  <svg {...base(p)}>
    <path d="M7 18h9.5a3.5 3.5 0 0 0 .5-6.96 5 5 0 0 0-9.6-1.2A4 4 0 0 0 7 18Z" />
  </svg>
);

export const IconTree = (p: P) => (
  <svg {...base(p)}>
    <rect x="9" y="3" width="6" height="5" rx="1.2" />
    <rect x="3" y="16" width="6" height="5" rx="1.2" />
    <rect x="15" y="16" width="6" height="5" rx="1.2" />
    <path d="M12 8v3M12 11H6v5M12 11h6v5" />
  </svg>
);

export const IconClock = (p: P) => (
  <svg {...base(p)}>
    <circle cx="12" cy="12" r="8.5" />
    <path d="M12 7.5V12l3 2" />
  </svg>
);

export const IconClose = (p: P) => (
  <svg {...base(p)}>
    <path d="M6 6l12 12M18 6 6 18" />
  </svg>
);

export const IconPin = (p: P) => (
  <svg {...base(p)}>
    <path d="m9 3 6 6M8 8l8-4 4 4-4 8-3-3-5 5" />
    <path d="m3.5 20.5 6-6" />
  </svg>
);

export const IconCopy = (p: P) => (
  <svg {...base(p)}>
    <rect x="8" y="8" width="11" height="11" rx="1.5" />
    <path d="M16 8V5.5A1.5 1.5 0 0 0 14.5 4h-10A1.5 1.5 0 0 0 3 5.5v10A1.5 1.5 0 0 0 4.5 17H8" />
  </svg>
);

export const IconTrash = (p: P) => (
  <svg {...base(p)}>
    <path d="M4 7h16M9.5 7V5.2A1.2 1.2 0 0 1 10.7 4h2.6a1.2 1.2 0 0 1 1.2 1.2V7M6.5 7l.8 12a1.5 1.5 0 0 0 1.5 1.4h6.4a1.5 1.5 0 0 0 1.5-1.4l.8-12" />
    <path d="M10 11v5M14 11v5" />
  </svg>
);

export const IconRestore = (p: P) => (
  <svg {...base(p)}>
    <path d="M4 8a8.5 8.5 0 1 1-1 6.5" />
    <path d="M4 3.5V8h4.5" />
  </svg>
);

export const IconLayout = (p: P) => (
  <svg {...base(p)}>
    <path d="M12 4v4M12 12v-4h-6v4M12 8h6v4" />
    <circle cx="12" cy="4" r="1.8" />
    <circle cx="6" cy="15" r="1.8" />
    <circle cx="18" cy="15" r="1.8" />
    <path d="M6 17v3M18 17v3" />
  </svg>
);

export const IconResizeHorizontal = (p: P) => (
  <svg {...base(p)}>
    <path d="M4 6v12M20 6v12M8 12h8" />
    <path d="m8 9-3 3 3 3M16 9l3 3-3 3" />
  </svg>
);

/** Two boxes lined up on a shared centreline. */
export const IconAlignGuides = (p: P) => (
  <svg {...base(p)}>
    <path d="M12 2v20" strokeDasharray="3 2.5" />
    <rect x="3" y="5" width="9" height="5" rx="1" />
    <rect x="12" y="14" width="9" height="5" rx="1" />
  </svg>
);

export const IconPlus = (p: P) => (
  <svg {...base(p)}>
    <path d="M12 5v14M5 12h14" />
  </svg>
);

export const IconWarning = (p: P) => (
  <svg {...base(p)}>
    <path d="M12 4 2.8 19.5h18.4L12 4Z" />
    <path d="M12 10v4.5M12 17.5v.1" />
  </svg>
);

export const IconCheck = (p: P) => (
  <svg {...base(p)}>
    <path d="M4.5 12.5 10 18 19.5 7" />
  </svg>
);

export const IconChevronDown = (p: P) => (
  <svg {...base(p)}>
    <path d="m6 9 6 6 6-6" />
  </svg>
);

export const IconChevronUp = (p: P) => (
  <svg {...base(p)}>
    <path d="m6 15 6-6 6 6" />
  </svg>
);

export const IconDoc = (p: P) => (
  <svg {...base(p)}>
    <path d="M7 3h7l4 4v14H7z" />
    <path d="M14 3v4h4M10 12h5M10 16h5" />
  </svg>
);

export const IconPages = (p: P) => (
  <svg {...base(p)}>
    <path d="M6 6V3h11l3 3v13h-3" />
    <path d="M4 7h10l3 3v11H4z" />
    <path d="M14 7v3h3M7 14h7M7 17h7" />
  </svg>
);

export const IconPopout = (p: P) => (
  <svg {...base(p)}>
    <path d="M14 4h6v6M20 4l-9 9" />
    <path d="M18 13v6H5V6h6" />
  </svg>
);

export const IconImport = (p: P) => (
  <svg {...base(p)}>
    <path d="M12 3v11M8 10l4 4 4-4" />
    <path d="M5 14v5h14v-5" />
  </svg>
);

export const IconFolder = (p: P) => (
  <svg {...base(p)}>
    <path d="M3.5 6.5h6l2 2h9v10.5a1.5 1.5 0 0 1-1.5 1.5H5A1.5 1.5 0 0 1 3.5 19z" />
  </svg>
);

export const IconFolderOpen = (p: P) => (
  <svg {...base(p)}>
    <path d="M3.5 7V5.5A1.5 1.5 0 0 1 5 4h4.5l2 2H19a1.5 1.5 0 0 1 1.5 1.5V9" />
    <path d="M3 9h18l-2 10.5H5z" />
  </svg>
);

export const IconBell = (p: P) => (
  <svg {...base(p)}>
    <path d="M6 16v-6a6 6 0 0 1 12 0v6l1.5 2.5H4.5L6 16Z" />
    <path d="M10 20.5a2 2 0 0 0 4 0" />
  </svg>
);

export const IconPencil = (p: P) => (
  <svg {...base(p)}>
    <path d="M4 20h4l10.5-10.5a2.5 2.5 0 0 0-3.5-3.5L4.5 16.5 4 20Z" />
    <path d="M14.5 6.5 17.5 9.5" />
  </svg>
);

export const IconEye = (p: P) => (
  <svg {...base(p)}>
    <path d="M2.5 12S6 5.5 12 5.5 21.5 12 21.5 12 18 18.5 12 18.5 2.5 12 2.5 12Z" />
    <circle cx="12" cy="12" r="3" />
  </svg>
);

export const IconSignOut = (p: P) => (
  <svg {...base(p)}>
    <path d="M14.5 4.5H6.5a2 2 0 0 0-2 2v11a2 2 0 0 0 2 2h8" />
    <path d="M18 15.5 21.5 12 18 8.5M21 12h-9.5" />
  </svg>
);

export const IconHelp = (p: P) => (
  <svg {...base(p)}>
    <circle cx="12" cy="12" r="8.5" />
    <path d="M9.5 9.5a2.5 2.5 0 1 1 3.6 2.2c-.8.4-1.1 1-1.1 1.8v.5M12 17.2v.1" />
  </svg>
);

export const IconGear = (p: P) => (
  <svg {...base(p)}>
    <circle cx="12" cy="12" r="3.2" />
    <path d="M12 2.8v2.6M12 18.6v2.6M21.2 12h-2.6M5.4 12H2.8M18.5 5.5l-1.8 1.8M7.3 16.7l-1.8 1.8M18.5 18.5l-1.8-1.8M7.3 7.3 5.5 5.5" />
  </svg>
);

export const IconParagraph = (p: P) => (
  <svg {...base(p)}>
    <path d="M13 4v16M17 4v16M13 4h-3.5a4.5 4.5 0 0 0 0 9H13" />
  </svg>
);

export const IconListBullet = (p: P) => (
  <svg {...base(p)}>
    <path d="M9 6h11M9 12h11M9 18h11" />
    <circle cx="4.5" cy="6" r="1.2" />
    <circle cx="4.5" cy="12" r="1.2" />
    <circle cx="4.5" cy="18" r="1.2" />
  </svg>
);

export const IconListOrdered = (p: P) => (
  <svg {...base(p)}>
    <path d="M9 6h11M9 12h11M9 18h11" />
    <path d="M4 4.5h1V8M3.5 11.5h2l-2 3h2M3.5 16.5h2v1.5h-2v1.5h2" />
  </svg>
);

export const IconListTask = (p: P) => (
  <svg {...base(p)}>
    <path d="M10 6h10M10 12h10M10 18h10" />
    <path d="m3 6 1.4 1.4L7 4.8M3 12l1.4 1.4L7 10.8M3 18l1.4 1.4L7 16.8" />
  </svg>
);

export const IconQuote = (p: P) => (
  <svg {...base(p)}>
    <path d="M9 7H5.5A1.5 1.5 0 0 0 4 8.5V12h5V7Zm0 0v4c0 3-1.5 5-4 6" />
    <path d="M19.5 7H16a1.5 1.5 0 0 0-1.5 1.5V12h5V7Zm0 0v4c0 3-1.5 5-4 6" />
  </svg>
);

export const IconLink = (p: P) => (
  <svg {...base(p)}>
    <path d="M10 13.5a4 4 0 0 0 5.7 0l2.8-2.8a4 4 0 0 0-5.7-5.7L11.5 6.4" />
    <path d="M14 10.5a4 4 0 0 0-5.7 0L5.5 13.3a4 4 0 0 0 5.7 5.7l1.3-1.3" />
  </svg>
);

export const IconImage = (p: P) => (
  <svg {...base(p)}>
    <rect x="3.5" y="5" width="17" height="14" rx="2" />
    <circle cx="9" cy="10" r="1.5" />
    <path d="m4.5 17 4.5-4.5 3.5 3.5 3-2.5 4 3.5" />
  </svg>
);

export const IconTable = (p: P) => (
  <svg {...base(p)}>
    <rect x="3.5" y="4.5" width="17" height="15" rx="1.5" />
    <path d="M3.5 9.5h17M3.5 14.5h17M9.5 4.5v15M15 4.5v15" />
  </svg>
);

export const IconExport = (p: P) => (
  <svg {...base(p)}>
    <path d="M13.5 3H7a1.5 1.5 0 0 0-1.5 1.5v15A1.5 1.5 0 0 0 7 21h10a1.5 1.5 0 0 0 1.5-1.5V8z" />
    <path d="M13.5 3v5h5" />
    <path d="M12 11.5v5.5M9.8 14.8 12 17l2.2-2.2" />
  </svg>
);

export const IconOutline = (p: P) => (
  <svg {...base(p)}>
    <path d="M4 6h16M7 12h13M10 18h10" />
  </svg>
);

export const IconMore = (p: P) => (
  <svg {...base(p)}>
    <circle cx="5.5" cy="12" r="1.3" />
    <circle cx="12" cy="12" r="1.3" />
    <circle cx="18.5" cy="12" r="1.3" />
  </svg>
);

// ---- node kind icons ----

export const IconPlay = (p: P) => (
  <svg {...base(p)}>
    <path d="M7 4.5v15l12-7.5L7 4.5Z" />
  </svg>
);

export const IconFlag = (p: P) => (
  <svg {...base(p)}>
    <path d="M6 21V4M6 4h12l-2.5 4L18 12H6" />
  </svg>
);

export const IconTask = (p: P) => (
  <svg {...base(p)}>
    <rect x="4" y="4" width="16" height="16" rx="2.5" />
    <path d="m8.5 12.5 2.5 2.5 5-5.5" />
  </svg>
);

export const IconScene = (p: P) => (
  <svg {...base(p)}>
    <rect x="3.5" y="5" width="17" height="14" rx="2" />
    <path d="M3.5 9h17M7.5 5v4M16.5 5v4" />
  </svg>
);

export const IconSpark = (p: P) => (
  <svg {...base(p)}>
    <path d="M12 3v5M12 16v5M3 12h5M16 12h5M6.5 6.5 9 9M15 15l2.5 2.5M17.5 6.5 15 9M9 15l-2.5 2.5" />
  </svg>
);

export const IconFork = (p: P) => (
  <svg {...base(p)}>
    <path d="M12 4v6c0 2-1.5 3-3.5 3S5 14 5 16v4M12 10c0 2 1.5 3 3.5 3s3.5 1 3.5 3v4" />
    <circle cx="12" cy="4" r="1.6" />
  </svg>
);

export const IconGate = (p: P) => (
  <svg {...base(p)}>
    <path d="M5 21V8a7 7 0 0 1 14 0v13M5 21h14M9 21v-5h6v5" />
  </svg>
);

export const kindIcon = (kind: string | undefined, size = 12) => {
  switch (kind) {
    case "start":
      return <IconPlay size={size} />;
    case "end":
      return <IconFlag size={size} />;
    case "scene":
      return <IconScene size={size} />;
    case "event":
      return <IconSpark size={size} />;
    case "choice":
      return <IconFork size={size} />;
    case "gate":
      return <IconGate size={size} />;
    default:
      return <IconTask size={size} />;
  }
};
