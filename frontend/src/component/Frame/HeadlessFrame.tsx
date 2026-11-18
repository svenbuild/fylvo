import { Box, Container, Grid, Paper } from "@mui/material";
import { shellSurface } from "../../fylvoTheme.ts";
import { Outlet, useNavigation } from "react-router-dom";
import { useAppSelector } from "../../redux/hooks.ts";
import AutoHeight from "../Common/AutoHeight.tsx";
import CircularProgress from "../Common/CircularProgress.tsx";
import LanguageSwitcher from "../Common/LanguageSwitcher.tsx";
import PoweredBy from "./PoweredBy.tsx";
import Logo from "../Common/Logo.tsx";
import { ConnectingLine, OAuthAppCard } from "./OauthAppCard.tsx";

const Loading = () => {
  return (
    <Box
      sx={{
        display: "flex",
        justifyContent: "center",
        py: 4,
      }}
    >
      <CircularProgress />
    </Box>
  );
};

const HeadlessFrame = () => {
  const loading = useAppSelector((state) => state.globalState.loading.headlessFrame);
  const { headless_footer, headless_bottom } = useAppSelector(
    (state) => state.siteConfig.basic?.config?.custom_html ?? {},
  );
  const oauthApp = useAppSelector((state) => state.globalState.oauthApp);
  const oauthAppLoading = useAppSelector((state) => state.globalState.oauthAppLoading);
  let navigation = useNavigation();

  const showOAuthCard = oauthApp || oauthAppLoading;

  return (
    <Box
      sx={{
        backgroundColor: shellSurface,
        flexGrow: 1,
        height: "100dvh",
        overflow: "auto",
      }}
    >
      <Container maxWidth={false} sx={{ maxWidth: 416, px: 2 }}>
        <Grid
          container
          spacing={0}
          direction="column"
          alignItems="center"
          justifyContent="center"
          sx={{ minHeight: "100dvh", py: 2 }}
        >
          <Box sx={{ width: "100%", mb: 1.5 }}>
            {/* OAuth App Card - shown above the login panel */}
            {showOAuthCard && (
              <>
                <OAuthAppCard app={oauthApp} loading={!!oauthAppLoading && !oauthApp} />
                <ConnectingLine />
              </>
            )}
            <Box
              sx={{
                display: "flex",
                alignItems: "center",
                justifyContent: "space-between",
                mb: 1,
                "& .MuiIconButton-root": { color: "text.secondary" },
                "@media (max-width: 599.95px), (pointer: coarse)": {
                  "& .MuiIconButton-root": { minWidth: 40, minHeight: 40 },
                },
              }}
            >
              <Logo sx={{ width: 112, maxHeight: 28, objectFit: "contain", objectPosition: "left" }} />
              <LanguageSwitcher />
            </Box>
            <Paper
              variant="outlined"
              sx={{
                p: "16px",
                bgcolor: (theme) => theme.palette.mode === "dark" ? theme.palette.grey[900] : theme.palette.background.paper,
                // Room for the label that sits on the top border of each field.
                "& .MuiFormControl-marginNormal": { mt: 2, mb: 0 },
                "& .MuiOutlinedInput-root:not(.MuiInputBase-multiline) > .MuiOutlinedInput-input": { py: "6px" },
                "& .MuiButton-outlined": { color: "text.primary" },
                "& .MuiButtonBase-root.Mui-focusVisible": {
                  outline: (theme) => `1px solid ${theme.palette.primary.main}`,
                  outlineOffset: -2,
                },
                "@media (max-width: 599.95px), (pointer: coarse)": {
                  "& .MuiButton-root, & .MuiOutlinedInput-root": { minHeight: 40 },
                },
              }}
            >
              <AutoHeight>
                <div>
                  <Box
                    sx={{
                      display: loading || navigation.state !== "idle" ? "none" : "block",
                    }}
                  >
                    <Outlet />
                    {headless_bottom && (
                      <Box sx={{ width: "100%" }}>
                        <div dangerouslySetInnerHTML={{ __html: headless_bottom }} />
                      </Box>
                    )}
                  </Box>
                  {(loading || navigation.state !== "idle") && <Loading />}
                </div>
              </AutoHeight>
            </Paper>
          </Box>
          <PoweredBy />
          {headless_footer && (
            <Box sx={{ width: "100%", mb: 2 }}>
              <div dangerouslySetInnerHTML={{ __html: headless_footer }} />
            </Box>
          )}
        </Grid>
      </Container>
    </Box>
  );
};

export default HeadlessFrame;
