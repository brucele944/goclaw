import { BrowserRouter } from "react-router";
import { MotionConfig } from "framer-motion";
import { AppProviders } from "@/components/providers/app-providers";
import { AppRoutes } from "@/routes";

export default function App() {
  return (
    <MotionConfig reducedMotion="user">
      <BrowserRouter>
        <AppProviders>
          <AppRoutes />
        </AppProviders>
      </BrowserRouter>
    </MotionConfig>
  );
}
