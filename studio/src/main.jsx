import React from "react";
import ReactDOM from "react-dom/client";
import { ConfigProvider } from "antd";
import "antd/dist/reset.css";
import App from "./App.jsx";
import "./styles.css";

const theme = {
  token: {
    colorPrimary: "#0051c3",
    colorInfo: "#0051c3",
    colorSuccess: "#168a60",
    colorWarning: "#b65c00",
    colorError: "#c2352a",
    colorText: "#202124",
    colorTextSecondary: "#5f6368",
    colorBorder: "#d9d9dc",
    colorBgLayout: "#f7f7f8",
    colorBgContainer: "#ffffff",
    borderRadius: 5,
    controlHeight: 32,
    fontSize: 13,
    fontFamily: 'Inter, "PingFang SC", "Microsoft YaHei", system-ui, sans-serif',
  },
  components: {
    Menu: {
      itemHeight: 36,
      itemBorderRadius: 4,
      itemSelectedBg: "#eef5ff",
      itemSelectedColor: "#003eaa",
    },
    Table: {
      headerBg: "#f5f5f6",
      headerColor: "#5f6368",
      cellPaddingBlock: 12,
      cellPaddingInline: 14,
    },
  },
};

ReactDOM.createRoot(document.getElementById("root")).render(
  <React.StrictMode>
    <ConfigProvider theme={theme}>
      <App />
    </ConfigProvider>
  </React.StrictMode>,
);
