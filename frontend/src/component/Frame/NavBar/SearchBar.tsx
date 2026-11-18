import { Box, Button, IconButton, styled, useMediaQuery, useTheme } from "@mui/material";
import { useHotkeys } from "react-hotkeys-hook";
import { Trans, useTranslation } from "react-i18next";
import { setSearchPopup } from "../../../redux/globalStateSlice.ts";
import { useAppDispatch } from "../../../redux/hooks.ts";
import { hoverSurface } from "../../../fylvoTheme.ts";
import Search from "../../Icons/Search.tsx";

export const KeyIndicator = styled("code")(({ theme }) => ({
  backgroundColor: theme.palette.mode === "light" ? theme.palette.grey[100] : theme.palette.grey[900],
  border: `1px solid ${theme.palette.divider}`,
  boxShadow:
    theme.palette.mode === "light"
      ? "0 1px 1px rgba(0, 0, 0, 0.2), 0 2px 0 0 rgba(255, 255, 255, 0.7) inset"
      : "0 1px 1px rgba(0, 0, 0, 0.2), 0 2px 0 0 #3d3e42 inset",
  padding: theme.spacing(0, 0.5),
  borderRadius: 4,
}));

const SearchButton = styled(Button)(({ theme }) => ({
  width: "clamp(360px, 27vw, 420px)",
  maxWidth: "100%",
  height: "100%",
  minWidth: 0,
  justifyContent: "flex-start",
  backgroundColor: theme.palette.grey[theme.palette.mode === "light" ? 50 : 900],
  color: theme.palette.text.secondary,
  "&:hover": {
    backgroundColor: hoverSurface(theme),
    color: theme.palette.text.primary,
  },
  "& .MuiButton-startIcon": { marginLeft: 0 },
}));

const SearchBar = () => {
  const dispatch = useAppDispatch();
  const theme = useTheme();
  const isMobile = useMediaQuery(theme.breakpoints.down("sm"));
  const { t } = useTranslation();
  useHotkeys(
    "/",
    () => {
      dispatch(setSearchPopup(true));
    },
    { preventDefault: true },
  );

  if (isMobile) {
    return (
      <IconButton aria-label={t("navbar.searchFilesTitle")} onClick={() => dispatch(setSearchPopup(true))}>
        <Search />
      </IconButton>
    );
  }

  return (
    <SearchButton onClick={() => dispatch(setSearchPopup(true))} variant="outlined" startIcon={<Search />}>
      <Box component="span" sx={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
        <Trans
          ns="application"
          i18nKey="navbar.searchPlaceholder"
          components={[<KeyIndicator sx={{ mx: 0.5, px: "4px", boxShadow: "none", fontSize: "0.75rem" }} />]}
        />
      </Box>
    </SearchButton>
  );
};

export default SearchBar;
