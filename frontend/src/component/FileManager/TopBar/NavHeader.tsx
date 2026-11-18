import { Stack, useMediaQuery, useTheme } from "@mui/material";
import Breadcrumb from "./Breadcrumb.tsx";
import TopActions from "./TopActions.tsx";
import { RadiusFrame } from "../../Frame/RadiusFrame.tsx";
import TopActionsSecondary from "./TopActionsSecondary.tsx";
import { SearchIndicator } from "../Search/SearchIndicator.tsx";

const NavHeader = () => {
  const theme = useTheme();
  const isMobile = useMediaQuery(theme.breakpoints.down("sm"));
  return (
    <Stack
      direction={"row"}
      sx={{
        px: { xs: 1.5, sm: 1 },
        gap: { xs: "4px", sm: "6px" },
        minHeight: 36,
        "@media (max-width: 599.95px), (pointer: coarse)": { minHeight: 40 },
        alignItems: "center",
        flexShrink: 0,
        borderBottom: 1,
        borderColor: "divider",
        bgcolor: "background.paper",
      }}
    >
      <RadiusFrame
        sx={{
          flexGrow: 1,
          minWidth: 0,
          bgcolor: "transparent",
          overflow: "hidden",
          display: "flex",
        }}
      >
        <Breadcrumb />
        <SearchIndicator />
      </RadiusFrame>
      {!isMobile && (
        <TopActionsSecondary />
      )}
      <TopActions />
    </Stack>
  );
};

export default NavHeader;
