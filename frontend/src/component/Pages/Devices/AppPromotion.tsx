import { Box, Button, Grid, Typography, useTheme } from "@mui/material";
import { QRCodeSVG } from "qrcode.react";
import { useMemo, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { useAppSelector } from "../../../redux/hooks.ts";
import SessionManager from "../../../session";

const AppPromotion = () => {
  const title = useAppSelector((state) => state.siteConfig.basic.config.title);
  const { t } = useTranslation();
  const [showQr, setShowQr] = useState(false);
  const theme = useTheme();
  const refreshToken = useMemo(() => {
    return (
      window.location.origin +
      "/login?refresh_token=" +
      (SessionManager.currentLoginOrNull()?.token?.refresh_token ?? "")
    );
  }, [showQr]);

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
      <Grid item container alignItems={"center"} xs={12} md={7}>
        <Box>
          <Box marginBottom={1}>
            <Typography
              variant="h6"
              color="text.primary"
            >
              <Trans
                i18nKey={"setting.connectByiOS"}
                values={{ title }}
                components={[
                  <Typography
                    key={0}
                    component="span" variant="inherit"
                  />,
                ]}
              />
            </Typography>
          </Box>
          <Box
            color="text.secondary"
            sx={{
              ol: {
                paddingInlineStart: "18px",
                my: 0,
              },
              li: {
                marginBottom: 1.25,
              },
            }}
          >
            <ol>
              <li>
                <Typography variant="body2" component="p">
                  {t("setting.downloadOurApp")}
                </Typography>
                <Box marginTop={1}>
                  <Box component={"a"} href={"https://apps.apple.com/us/app/cloudreve/id1619480823"} target={"_blank"}>
                    <Box component={"img"} src={"/static/img/appstore.svg"} sx={{ height: 30 }} />
                  </Box>
                </Box>
              </li>
              <li>
                <Typography variant="body2" component="p">
                  {t("setting.fillInEndpoint")}
                </Typography>
                <Box
                  marginTop={1}
                  sx={{
                    position: "relative",
                    display: "inline-block",
                  }}
                >
                  <QRCodeSVG
                    marginSize={4}
                    size={144}
                    onClick={() => setShowQr(false)}
                    style={{
                      transition: "all .3s ease-in",
                      filter: showQr ? "initial" : "blur(8px)",
                      backgroundColor: theme.palette.common.white,
                    }}
                    value={refreshToken}
                  />

                  {!showQr && (
                    <Button
                      onClick={() => setShowQr(true)}
                      sx={{
                        position: "absolute",
                        top: "50%",
                        left: "50%",
                        transform: "translate(-50%, -50%)",
                      }}
                      variant="contained"
                    >
                      {t("setting.shoeQr")}
                    </Button>
                  )}
                </Box>
              </li>
              <li>
                <Typography variant="body2" component="p">
                  {t("setting.loginApp")}
                </Typography>
              </li>
            </ol>
          </Box>
        </Box>
      </Grid>
      <Grid item xs={12} md={5} sx={{ display: "grid", placeItems: "center", pt: { xs: 1, md: 0 } }}>
        <Box
          component="img"
          src="https://cloudreve.org/imgs/ios/766shots_so.png"
          alt="Image Description"
          sx={{ display: "block", width: "100%", maxWidth: 240 }}
        />
      </Grid>
    </Grid>
  );
};

export default AppPromotion;
