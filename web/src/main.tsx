import React from "react";
import ReactDOM from "react-dom/client";
import App from "./App";
import { ViewErrorBoundary } from "./components/ViewErrorBoundary";
import "./app.css";

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <ViewErrorBoundary>
      <App />
    </ViewErrorBoundary>
  </React.StrictMode>,
);
