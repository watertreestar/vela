import React from "react";
import ReactDOM from "react-dom/client";
import { ConfigProvider } from "antd";
import "antd/dist/reset.css";
import App from "./App.jsx";
import "./styles.css";

const theme = {
  token: {
    colorPrimary: "#c95d16",
    colorInfo: "#3568a8",
    colorSuccess: "#1a7f5a",
    colorWarning: "#b66a00",
    colorError: "#c33d35",
    colorText: "#202b38",
    colorTextSecondary: "#667085",
    colorBorder: "#d9dde3",
    colorBgLayout: "#f4f5f7",
    colorBgContainer: "#ffffff",
    borderRadius: 6,
    controlHeight: 32,
    fontSize: 13,
    fontFamily: 'Inter, "PingFang SC", "Microsoft YaHei", system-ui, sans-serif',
  },
  components: {
    Menu: {
      itemHeight: 36,
      itemBorderRadius: 4,
      itemSelectedBg: "#fff1e6",
      itemSelectedColor: "#a64909",
    },
    Table: {
      headerBg: "#f7f8fa",
      headerColor: "#5d6878",
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
