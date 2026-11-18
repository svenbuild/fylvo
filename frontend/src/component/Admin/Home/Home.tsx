import Giscus from "@giscus/react";
import { GitHub } from "@mui/icons-material";
import {
  Avatar,
  Box,
  Container,
  Divider,
  List,
  ListItem,
  ListItemAvatar,
  ListItemButton,
  ListItemIcon,
  ListItemText,
  Paper,
  Skeleton,
  styled,
  Typography,
} from "@mui/material";
import Grid from "@mui/material/Grid";
import { useTheme } from "@mui/material/styles";
import dayjs from "dayjs";
import i18next from "i18next";
import { useCallback, useEffect, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { CSSTransition, SwitchTransition } from "react-transition-group";
import { CartesianGrid, Legend, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { getDashboardSummary } from "../../../api/api.ts";
import { HomepageSummary } from "../../../api/dashboard.ts";
import { useAppDispatch } from "../../../redux/hooks.ts";
import FacebookCircularProgress from "../../Common/CircularProgress.tsx";
import { SecondaryButton, SquareChip } from "../../Common/StyledComponents.tsx";
import TimeBadge from "../../Common/TimeBadge.tsx";
import Book from "../../Icons/Book.tsx";
import BoxMultipleFilled from "../../Icons/BoxMultipleFilled.tsx";
import Discord from "../../Icons/Discord.tsx";
import DocumentCopyFilled from "../../Icons/DocumentCopyFilled.tsx";
import HomeIcon from "../../Icons/Home.tsx";
import OpenFilled from "../../Icons/OpenFilled.tsx";
import PeopleFilled from "../../Icons/PeopleFilled.tsx";
import ShareFilled from "../../Icons/ShareFilled.tsx";
import SparkleFilled from "../../Icons/SparkleFilled.tsx";
import Telegram from "../../Icons/Telegram.tsx";
import AdminPage from "../Common/AdminPage.tsx";
import PageHeader from "../../Pages/PageHeader.tsx";
import ProDialog from "../Common/ProDialog.tsx";
import SiteUrlWarning from "./SiteUrlWarning.tsx";
import CommentMultiple from "../../Icons/CommentMultiple.tsx";

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

const StyledListItemIcon = styled(ListItemIcon)(() => ({
  minWidth: 0,
}));

const Home = () => {
  const { t } = useTranslation("dashboard");
  const theme = useTheme();
  const dispatch = useAppDispatch();
  const [summary, setSummary] = useState<HomepageSummary | undefined>();
  const [chartLoading, setChartLoading] = useState(false);
  const [siteUrlWarning, setSiteUrlWarning] = useState(false);
  const [proDialogOpen, setProDialogOpen] = useState(false);
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
      <ProDialog open={proDialogOpen} onClose={() => setProDialogOpen(false)} />
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
          <Grid item xs={12} md={4} lg={3}>
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
          </Grid>
          <Grid item xs={12} md={5} lg={4}>
            <StyledPaper sx={{ p: 0 }}>
              <Box sx={{ p: 2, display: "flex", alignItems: "center" }}>
                <Box component={"img"} sx={{ width: 38 }} alt="Fylvo" src={"/static/img/fylvo.svg"} />
                <Box sx={{ ml: 1.5 }}>
                  <Typography variant={"body2"} fontWeight={600}>
                    Fylvo
                    {summary && summary.version.pro && (
                      <SquareChip sx={{ ml: 1, height: "initial" }} size={"small"} color={"primary"} label={"Pro"} />
                    )}
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
              <Divider />
              <List component="nav" aria-label="main mailbox folders" sx={{ mx: 1, py: 0.5, "& .MuiListItemText-root": { my: 0 }, "& .MuiListItemButton-root": { minHeight: { xs: 40, sm: 28 } } }}>
                <ListItemButton onClick={() => window.open("https://cloudreve.org")}>
                  <ListItemIcon>
                    <HomeIcon />
                  </ListItemIcon>
                  <ListItemText primary={t("summary.homepage")} />
                  <StyledListItemIcon>
                    <OpenFilled />
                  </StyledListItemIcon>
                </ListItemButton>
                <ListItemButton onClick={() => window.open("https://github.com/cloudreve/cloudreve")}>
                  <ListItemIcon>
                    <GitHub />
                  </ListItemIcon>
                  <ListItemText primary={t("summary.github")} />
                  <StyledListItemIcon>
                    <OpenFilled />
                  </StyledListItemIcon>
                </ListItemButton>
                <ListItemButton onClick={() => window.open("https://docs.cloudreve.org/")}>
                  <ListItemIcon>
                    <Book />
                  </ListItemIcon>
                  <ListItemText primary={t("summary.documents")} />
                  <StyledListItemIcon>
                    <OpenFilled />
                  </StyledListItemIcon>
                </ListItemButton>
                <ListItemButton onClick={() => window.open("https://discord.gg/WTpMFpZT76")}>
                  <ListItemIcon>
                    <Discord />
                  </ListItemIcon>
                  <ListItemText primary={t("summary.discordCommunity")} />
                  <StyledListItemIcon>
                    <OpenFilled />
                  </StyledListItemIcon>
                </ListItemButton>
                <ListItemButton onClick={() => window.open("https://t.me/cloudreve_official")}>
                  <ListItemIcon>
                    <Telegram />
                  </ListItemIcon>
                  <ListItemText primary={t("summary.telegram")} />
                  <StyledListItemIcon>
                    <OpenFilled />
                  </StyledListItemIcon>
                </ListItemButton>
                <ListItemButton onClick={() => window.open("https://github.com/cloudreve/cloudreve/discussions")}>
                  <ListItemIcon>
                    <CommentMultiple />
                  </ListItemIcon>
                  <ListItemText primary={t("summary.forum")} />
                  <StyledListItemIcon>
                    <OpenFilled />
                  </StyledListItemIcon>
                </ListItemButton>
                {summary && !summary.version.pro && (
                  <ListItemButton onClick={() => setProDialogOpen(true)}>
                    <ListItemIcon>
                      <SparkleFilled color={"primary"} />
                    </ListItemIcon>
                    <ListItemText primary={t("summary.buyPro")} />
                  </ListItemButton>
                )}
              </List>
            </StyledPaper>
          </Grid>
          <Grid item xs={12} md={7} lg={8}>
            <StyledPaper>
              <Typography variant={"subtitle1"} fontWeight={500}>
                公告
              </Typography>
              <Divider sx={{ my: 1 }} />
              <Giscus
                id="comments"
                repo="cloudreve/cloudreve"
                repoId="MDEwOlJlcG9zaXRvcnkxMjAxNTYwNzY="
                mapping={"number"}
                term={i18next.language == "zh-CN" ? "2170" : "2169"}
                reactionsEnabled={"1"}
                emitMetadata={"0"}
                inputPosition={"bottom"}
                theme={theme.palette.mode === "dark" ? "dark" : "light"}
                lang={i18next.language == "zh-CN" ? "zh-CN" : "en"}
                loading={"lazy"}
              />
            </StyledPaper>
          </Grid>
        </Grid>
      </Container>
    </AdminPage>
  );
};

export default Home;
