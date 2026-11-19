import {
  Avatar,
  Box,
  Container,
  Divider,
  List,
  ListItem,
  ListItemAvatar,
  ListItemText,
  Paper,
  Skeleton,
  styled,
  Typography,
} from "@mui/material";
import Grid from "@mui/material/Grid";
import { useTheme } from "@mui/material/styles";
import dayjs from "dayjs";
import { useCallback, useEffect, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { CSSTransition, SwitchTransition } from "react-transition-group";
import { CartesianGrid, Legend, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { getDashboardSummary } from "../../../api/api.ts";
import { HomepageSummary } from "../../../api/dashboard.ts";
import { useAppDispatch } from "../../../redux/hooks.ts";
import FacebookCircularProgress from "../../Common/CircularProgress.tsx";
import { SecondaryButton } from "../../Common/StyledComponents.tsx";
import TimeBadge from "../../Common/TimeBadge.tsx";
import BoxMultipleFilled from "../../Icons/BoxMultipleFilled.tsx";
import DocumentCopyFilled from "../../Icons/DocumentCopyFilled.tsx";
import PeopleFilled from "../../Icons/PeopleFilled.tsx";
import ShareFilled from "../../Icons/ShareFilled.tsx";
import AdminPage from "../Common/AdminPage.tsx";
import PageHeader from "../../Pages/PageHeader.tsx";
import SiteUrlWarning from "./SiteUrlWarning.tsx";

const StyledPaper = styled(Paper)(({ theme }) => ({
  padding: 14,
  boxShadow: "none",
  "& > .MuiTypography-subtitle1": {
    fontSize: theme.typography.caption.fontSize,
    fontWeight: 600,
    color: theme.palette.text.secondary,
  },
  border: "1px solid " + theme.palette.divider,
}));

const Home = () => {
  const { t } = useTranslation("dashboard");
  const theme = useTheme();
  const dispatch = useAppDispatch();
  const [summary, setSummary] = useState<HomepageSummary | undefined>();
  const [chartLoading, setChartLoading] = useState(false);
  const [siteUrlWarning, setSiteUrlWarning] = useState(false);
  useEffect(() => {
    loadSummary(false);
  }, []);

  const loadSummary = useCallback((loadChart?: boolean) => {
    if (loadChart) {
      setChartLoading(true);
    }
    dispatch(getDashboardSummary(loadChart))
      .then((r) => {
        setSummary(r);
        if (!loadChart) {
          const target = r.site_urls.find((site) => site == window.location.origin);
          if (!target) {
            setSiteUrlWarning(true);
          }
        }
      })
      .finally(() => {
        setChartLoading(false);
      });
  }, []);

  return (
    <AdminPage>
      <SiteUrlWarning
        open={siteUrlWarning}
        onClose={() => setSiteUrlWarning(false)}
        existingUrls={summary?.site_urls ?? []}
      />
      <Container maxWidth="xl">
        <PageHeader title={t("nav.summary")} />
        <Grid container spacing={2}>
          <Grid alignContent={"stretch"} item xs={12} md={8} lg={9}>
            <StyledPaper>
              <Typography
                variant={"subtitle1"}
                fontWeight={500}
                sx={{
                  display: "flex",
                  justifyContent: "space-between",
                  alignItems: "center",
                }}
              >
                {t("summary.trend")}
                <Typography variant={"body2"} color={"text.secondary"}>
                  {summary?.metrics_summary?.generated_at && (
                    <Trans
                      i18nKey={"summary.generatedAt"}
                      ns={"dashboard"}
                      components={[<TimeBadge datetime={summary?.metrics_summary?.generated_at} variant={"inherit"} />]}
                    />
                  )}
                </Typography>
              </Typography>
              <Divider sx={{ my: 1 }} />
              <SwitchTransition>
                <CSSTransition
                  addEndListener={(node, done) => node.addEventListener("transitionend", done, false)}
                  classNames="fade"
                  key={`${!!summary?.metrics_summary}-${!!chartLoading}`}
                >
                  <Box>
                    {summary?.metrics_summary && (
                      <ResponsiveContainer width="100%" height={216}>
                        <LineChart
                          data={summary?.metrics_summary.dates.map((i, d) => ({
                            name: dayjs(i).format("MM-DD"),
                            user: summary?.metrics_summary?.users[d] ?? 0,
                            file: summary?.metrics_summary?.files[d] ?? 0,
                            share: summary?.metrics_summary?.shares[d] ?? 0,
                          }))}
                        >
                          <CartesianGrid stroke={theme.palette.divider} vertical={false} />
                          <XAxis dataKey="name" tick={{ fill: theme.palette.text.secondary, fontSize: 11 }} tickLine={false} />
                          <YAxis
                            allowDecimals={false}
                            tick={{ fill: theme.palette.text.secondary, fontSize: 11 }}
                            tickLine={false}
                            width={(() => {
                              const yAxisValue = [
                                ...(summary?.metrics_summary?.users ?? []),
                                ...(summary?.metrics_summary?.files ?? []),
                                ...(summary?.metrics_summary?.shares ?? []),
                              ];
                              const yAxisUpperLimit = yAxisValue.length ? Math.max(...yAxisValue) / 0.8 - 1 : 0;
                              const yAxisDigits = yAxisUpperLimit > 1 ? Math.floor(Math.log10(yAxisUpperLimit)) + 1 : 1;
                              return 3 + yAxisDigits * 9;
                            })()}
                          />
                          <Tooltip
                            contentStyle={{ background: theme.palette.background.paper, borderColor: theme.palette.divider }}
                            itemStyle={{ color: theme.palette.text.primary }}
                          />
                          <Legend />
                          <Line name={t("nav.users")} type="monotone" dataKey="user" stroke={theme.palette.primary.main} dot={false} />
                          <Line name={t("nav.files")} type="monotone" dataKey="file" stroke={theme.palette.warning.main} dot={false} />
                          <Line name={t("nav.shares")} type="monotone" dataKey="share" stroke={theme.palette.success.main} dot={false} />
                        </LineChart>
                      </ResponsiveContainer>
                    )}
                    {chartLoading && (
                      <Box
                        sx={{
                          height: 216,
                          display: "flex",
                          alignItems: "center",
                          justifyContent: "center",
                        }}
                      >
                        <FacebookCircularProgress />
                      </Box>
                    )}
                    {!summary?.metrics_summary?.generated_at && !chartLoading && (
                      <Box
                        sx={{
                          height: 216,
                          display: "flex",
                          alignItems: "center",
                          justifyContent: "center",
                        }}
                      >
                        <SecondaryButton onClick={() => loadSummary(true)}>
                          {t("application:fileManager.calculate")}
                        </SecondaryButton>
                      </Box>
                    )}
                  </Box>
                </CSSTransition>
              </SwitchTransition>
            </StyledPaper>
          </Grid>
          {/* Fylvo: the version card sits under the summary, so it does not stand alone in a row. */}
          <Grid item xs={12} md={4} lg={3} sx={{ display: "flex", flexDirection: "column", gap: 2 }}>
            <StyledPaper>
              <Typography variant={"subtitle1"} fontWeight={500}>
                {t("summary.summary")}
              </Typography>
              <Divider sx={{ my: 1 }} />
              <SwitchTransition>
                <CSSTransition
                  addEndListener={(node, done) => node.addEventListener("transitionend", done, false)}
                  classNames="fade"
                  key={`${!!summary?.metrics_summary}-${chartLoading}`}
                >
                  <Box>
                    {summary?.metrics_summary && (
                      <List
                        disablePadding
                        sx={{
                          display: "grid",
                          gridTemplateColumns: { xs: "repeat(2, minmax(0, 1fr))", md: "1fr" },
                          gap: 0.5,
                          "& .MuiListItem-root": { p: 0.75, minWidth: 0, border: 1, borderColor: "divider", borderRadius: 1 },
                          "& .MuiListItemAvatar-root": { minWidth: 26 },
                          "& .MuiListItemText-root": { my: 0 },
                          "& .MuiAvatar-root": { width: 20, height: 20, bgcolor: "transparent", color: "text.secondary" },
                          "& .MuiSvgIcon-root": { fontSize: 16 },
                        }}
                      >
                        <ListItem>
                          <ListItemAvatar>
                            <Avatar>
                              <PeopleFilled />
                            </Avatar>
                          </ListItemAvatar>
                          <ListItemText
                            secondary={t("summary.totalUsers")}
                            primary={summary.metrics_summary.user_total.toLocaleString()}
                          />
                        </ListItem>
                        <ListItem>
                          <ListItemAvatar>
                            <Avatar>
                              <DocumentCopyFilled />
                            </Avatar>
                          </ListItemAvatar>
                          <ListItemText
                            secondary={t("summary.totalFilesAndFolders")}
                            primary={summary.metrics_summary.file_total.toLocaleString()}
                          />
                        </ListItem>
                        <ListItem>
                          <ListItemAvatar>
                            <Avatar>
                              <ShareFilled />
                            </Avatar>
                          </ListItemAvatar>
                          <ListItemText
                            secondary={t("summary.shareLinks")}
                            primary={summary.metrics_summary.share_total.toLocaleString()}
                          />
                        </ListItem>
                        <ListItem>
                          <ListItemAvatar>
                            <Avatar>
                              <BoxMultipleFilled />
                            </Avatar>
                          </ListItemAvatar>
                          <ListItemText
                            secondary={t("summary.totalBlobs")}
                            primary={summary.metrics_summary.entities_total.toLocaleString()}
                          />
                        </ListItem>
                      </List>
                    )}
                    {chartLoading && (
                      <Box
                        sx={{
                          minHeight: 48,
                          display: "flex",
                          alignItems: "center",
                          justifyContent: "center",
                        }}
                      >
                        <FacebookCircularProgress />
                      </Box>
                    )}
                    {!summary?.metrics_summary?.generated_at && !chartLoading && (
                      <Box
                        sx={{
                          minHeight: 48,
                          display: "flex",
                          alignItems: "center",
                          justifyContent: "center",
                        }}
                      >
                        <SecondaryButton onClick={() => loadSummary(true)}>
                          {t("application:fileManager.calculate")}
                        </SecondaryButton>
                      </Box>
                    )}
                  </Box>
                </CSSTransition>
              </SwitchTransition>
            </StyledPaper>
            <StyledPaper sx={{ p: 0 }}>
              <Box sx={{ p: 2, display: "flex", alignItems: "center" }}>
                <Box component={"img"} sx={{ width: 38 }} alt="Fylvo" src={"/static/img/fylvo.svg"} />
                <Box sx={{ ml: 1.5 }}>
                  <Typography variant={"body2"} fontWeight={600}>
                    Fylvo
                  </Typography>
                  <Typography variant={"subtitle2"} color={"text.secondary"}>
                    {summary ? summary.version.version : <Skeleton variant={"text"} width={70} />}
                    {summary && (
                      <Box component={"span"} sx={{ ml: 1, color: (t) => t.palette.action.disabled }}>
                        #{summary.version.commit}
                      </Box>
                    )}
                  </Typography>
                </Box>
              </Box>
            </StyledPaper>
          </Grid>
        </Grid>
      </Container>
    </AdminPage>
  );
};

export default Home;
