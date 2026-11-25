import { hoverSurface, shellSurface } from "../../../fylvoTheme.ts";
import { AppBar, Box, Collapse, IconButton, Stack, Toolbar, Tooltip, useMediaQuery, useTheme } from "@mui/material";
import { Menu } from "@mui/icons-material";
import { useAppDispatch, useAppSelector } from "../../../redux/hooks.ts";
import { setDrawerOpen, setMobileDrawerOpen } from "../../../redux/globalStateSlice.ts";
import NewButton from "../../FileManager/NewButton.tsx";
import UserAction from "./UserAction.tsx";
import Setting from "../../Icons/Setting.tsx";
import DarkThemeSwitcher from "./DarkThemeSwitcher.tsx";
import NavBarMainActions from "./NavBarMainActions.tsx";
import MusicPlayer from "../../Viewers/MusicPlayer/MusicPlayer.tsx";
import { TaskListIconButton } from "../../Uploader/TaskListIconButton.tsx";
import { useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import SessionManager from "../../../session";
import { useContext, useState } from "react";
import { DrawerPopover } from "./AppDrawer.tsx";
import { PageVariant, PageVariantContext } from "../NavBarFrame.tsx";

const TopAppBar = () => {
  const dispatch = useAppDispatch();
  const theme = useTheme();
  const pageVariant = useContext(PageVariantContext);
  const isMobile = useMediaQuery(theme.breakpoints.down("sm"));
  const isMainPage = pageVariant == PageVariant.default;
  const { t } = useTranslation();
  const navigate = useNavigate();
  const open = useAppSelector((state) => state.globalState.drawerOpen);
  const mobileDrawerOpen = useAppSelector((state) => state.globalState.mobileDrawerOpen);
  const drawerWidth = useAppSelector((state) => state.globalState.drawerWidth);
  const musicPlayer = useAppSelector((state) => state.globalState.musicPlayer);
  const [mobileMenuAnchor, setMobileMenuAnchor] = useState<null | HTMLElement>(null);

  const appBarBg = shellSurface(theme);
  const isLogin = !!SessionManager.currentLoginOrNull();

  const onMobileMenuClicked = (e: React.MouseEvent<HTMLElement>) => {
    setMobileMenuAnchor(e.currentTarget);
    dispatch(setMobileDrawerOpen(true));
  };

  const onCloseMobileMenu = () => {
    dispatch(setMobileDrawerOpen(false));
  };

  // @ts-ignore
  return (
    <AppBar
      elevation={0}
      enableColorOnDark
      sx={(theme) => ({
        transition: theme.transitions.create(["margin", "width"], {
          easing: theme.transitions.easing.sharp,
          duration: theme.transitions.duration.leavingScreen,
        }),
        backgroundColor: appBarBg,
        color: theme.palette.getContrastText(appBarBg),
        ...(open &&
          !isMobile && {
            width: `calc(100% - ${drawerWidth}px)`,
            marginLeft: `${drawerWidth}px`,
            transition: theme.transitions.create(["margin", "width"], {
              easing: theme.transitions.easing.easeOut,
              duration: theme.transitions.duration.enteringScreen,
            }),
          }),
      })}
      position="fixed"
    >
      <Toolbar
        disableGutters
        sx={(theme) => ({
          "&&": {
            minHeight: 44,
            [theme.breakpoints.down("sm")]: { minHeight: 48 },
            "@media (pointer: coarse)": { minHeight: 48 },
          },
          pl: open && !isMobile ? 0 : 2,
          pr: 2,
          "& .MuiIconButton-root": {
            width: 30,
            height: 30,
            color: theme.palette.text.secondary,
            // Fylvo: only real pointers hover, a finger left the last tapped button highlighted.
            "@media (hover: hover)": {
              "&:hover": { backgroundColor: hoverSurface(theme), color: theme.palette.text.primary },
            },
            [theme.breakpoints.down("sm")]: { width: 40, height: 40 },
            "@media (pointer: coarse)": { width: 40, height: 40 },
          },
          "& .MuiButtonBase-root.Mui-focusVisible": {
            outline: `1px solid ${theme.palette.primary.main}`,
            outlineOffset: -1,
          },
        })}
      >
        <Collapse orientation={"horizontal"} in={!open || isMobile}>
          <IconButton
            color="inherit"
            aria-label="open drawer"
            // @ts-ignore
            onClick={isMobile ? onMobileMenuClicked : () => dispatch(setDrawerOpen(true))}
            sx={{ mr: 1 }}
          >
            <Menu />
          </IconButton>
        </Collapse>
        {isMobile && (
          <>
            <DrawerPopover open={!!mobileDrawerOpen} anchorEl={mobileMenuAnchor} onClose={onCloseMobileMenu} />
          </>
        )}
        {!isMobile && isMainPage && (
          <Stack
            direction="row"
            spacing={1}
            sx={{
              height: 32,
              minWidth: 0,
              "& > .MuiButton-root": { flexShrink: 0 },
              "@media (pointer: coarse)": { height: 40 },
            }}
          >
            <NewButton />
            <NavBarMainActions />
          </Stack>
        )}
        <Box sx={{ flexGrow: 1 }} />
        <Stack
          direction={"row"}
          sx={{
            alignItems: "center",
            flexShrink: 0,
          }}
          spacing={0.5}
        >
          {!isMobile && <TaskListIconButton />}
          {musicPlayer && <MusicPlayer />}
          {!isMobile ? (
            <>
              <DarkThemeSwitcher />
              {isLogin && (
                <Tooltip title={t("navbar.setting")}>
                  <IconButton onClick={() => navigate("/settings")}>
                    <Setting />
                  </IconButton>
                </Tooltip>
              )}
              <UserAction />
            </>
          ) : (
            <>
              {isMainPage && <NavBarMainActions />}
              {isMainPage && <NewButton />}
              <UserAction />
            </>
          )}
        </Stack>
      </Toolbar>
    </AppBar>
  );
};

export default TopAppBar;
