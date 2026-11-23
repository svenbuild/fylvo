import { Box, Drawer, Popover, PopoverProps, Stack, useMediaQuery, useTheme } from "@mui/material";
import { shellSurface } from "../../../fylvoTheme.ts";
import { useContext, useRef } from "react";
import { useAppSelector } from "../../../redux/hooks.ts";
import SessionManager from "../../../session";
import TreeNavigation from "../../FileManager/TreeView/TreeNavigation.tsx";
import { PageVariant, PageVariantContext } from "../NavBarFrame.tsx";
import DrawerHeader from "./DrawerHeader.tsx";
import PageNavigation, { AdminPageNavigation } from "./PageNavigation.tsx";
import StorageSummary from "./StorageSummary.tsx";

const DrawerContent = () => {
  const { sidebar_bottom } = useAppSelector((state) => state.siteConfig.basic?.config?.custom_html ?? {});
  const scrollRef = useRef<any>();
  const user = SessionManager.currentLoginOrNull();
  const theme = useTheme();
  const isMobile = useMediaQuery(theme.breakpoints.down("sm"));
  const pageVariant = useContext(PageVariantContext);
  const isDashboard = pageVariant === PageVariant.dashboard;
  return (
    <>
      <DrawerHeader customize={!!user && !isDashboard} />
      <Stack
        direction={"column"}
        ref={scrollRef}
        sx={{
          p: "0 8px 8px",
          gap: "8px",
          flexGrow: 1,
          minHeight: 0,
          overflow: "auto",
        }}
      >
        {!isDashboard && (
          <>
            <TreeNavigation scrollRef={scrollRef} hideWithDrawer={!isMobile} />
            <PageNavigation />
            {user && <StorageSummary />}
          </>
        )}
        {isDashboard && <AdminPageNavigation />}
        {sidebar_bottom && (
          <Box sx={{ width: "100%" }}>
            <div dangerouslySetInnerHTML={{ __html: sidebar_bottom }} />
          </Box>
        )}
      </Stack>
    </>
  );
};

export const DrawerPopover = (props: PopoverProps) => {
  return (
    <Popover anchorOrigin={{ vertical: "bottom", horizontal: "left" }} {...props}>
      <Box
        sx={{
          display: "flex",
          flexDirection: "column",
          width: "min(300px, calc(100vw - 32px))",
          maxHeight: "calc(100dvh - 68px)",
        }}
      >
        <DrawerContent />
      </Box>
    </Popover>
  );
};

const AppDrawer = () => {
  const theme = useTheme();
  const open = useAppSelector((state) => state.globalState.drawerOpen);
  const drawerWidth = useAppSelector((state) => state.globalState.drawerWidth);
  const appBarBg = shellSurface(theme);

  return (
    <Drawer
      sx={{
        width: drawerWidth,
        flexShrink: 0,
        display: "flex",
        "& .MuiDrawer-paper": {
          width: drawerWidth,
          boxSizing: "border-box",
          backgroundColor: appBarBg,
          borderRight: "initial",
        },
      }}
      variant="persistent"
      anchor="left"
      open={open}
    >
      <DrawerContent />
    </Drawer>
  );
};

export default AppDrawer;
