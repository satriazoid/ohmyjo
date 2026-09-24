import { createRoot } from "react-dom/client";
import { App } from "./components/App";
import { useApp } from "./store";
import { Transport } from "./transport";
import "./styles.css";

const root = document.getElementById("root");
if (!root) throw new Error("#root is missing from index.html");

// One socket for the whole window. The URL is derived from the served origin
// so the app works identically embedded in the WebView and in a browser tab
// pointed at the loopback server.
const scheme = location.protocol === "https:" ? "wss" : "ws";
useApp.getState().connect(new Transport(`${scheme}://${location.host}/ws`));

createRoot(root).render(<App />);
