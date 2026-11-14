// DEV ONLY: the entry of /theme-preview.html (see ThemePreview.tsx). Not an
// input of `npm run build`, so it never reaches the built app.

import React from "react";
import ReactDOM from "react-dom/client";

import "../app.css";
import { ThemePreview } from "./ThemePreview";

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <ThemePreview />
  </React.StrictMode>,
);
