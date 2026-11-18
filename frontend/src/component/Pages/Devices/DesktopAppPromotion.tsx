import { Box, Button, Grid, Typography } from "@mui/material";
import { useTranslation } from "react-i18next";
import { useAppSelector } from "../../../redux/hooks.ts";

const DesktopAppPromotion = () => {
  const title = useAppSelector((state) => state.siteConfig.basic.config.title);
  const { t } = useTranslation();

  return (
    <Grid
      container
      sx={{
        mt: 2,
        p: "14px",
        border: 1,
        borderColor: "divider",
        borderRadius: 1,
      }}
    >
      <Grid item container alignItems={"center"} xs={12} md={6}>
        <Box sx={{ pr: { md: 2 } }}>
          <Box marginBottom={1}>
            <Typography variant="h6">
              {t("setting.desktopAppTitleLead")}
              {title}
              {t("setting.desktopAppTitleTrail")}
            </Typography>
          </Box>
          <Box marginBottom={1}>
            <Typography variant="body2" component="p" color="text.secondary">
              {t("setting.desktopAppDescription")}
            </Typography>
          </Box>
          <Button
            component="a"
            href="https://apps.microsoft.com/detail/9P3GH5RNNZFD?referrer=appbadge&mode=direct"
            target="_blank"
            variant="outlined"
            color="inherit"
          >
            Microsoft Store
          </Button>
        </Box>
      </Grid>
      <Grid item xs={12} md={6} sx={{ pt: { xs: 2, md: 0 }, alignSelf: "center" }}>
        <Box
          sx={{
            display: "grid",
            gridTemplateColumns: "minmax(0, 2fr) minmax(0, 1fr)",
            alignItems: "end",
            gap: 1,
            "& > img": { display: "block", width: "100%", border: 1, borderColor: "divider", borderRadius: 1 },
          }}
        >
          <Box
            component="img"
            src="https://cloudreve.org/imgs/desktop/explorer.png"
            alt="Windows Explorer Integration"
          />
          <Box
            component="img"
            src="https://cloudreve.org/imgs/desktop/sync-ui.png"
            alt="Sync UI"
          />
        </Box>
      </Grid>
    </Grid>
  );
};

export default DesktopAppPromotion;
