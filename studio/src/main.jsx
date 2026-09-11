import React from "react";
import ReactDOM from "react-dom/client";
import App from "./App.jsx";
import { TooltipProvider } from "./components/admin/primitives";
import "./styles.css";

ReactDOM.createRoot(document.getElementById("root")).render(
  <React.StrictMode>
    <TooltipProvider>
      <App />
    </TooltipProvider>
  </React.StrictMode>,
);
