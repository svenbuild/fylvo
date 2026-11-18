import * as React from "react";
import { useCallback, useEffect, useRef, useState } from "react";
import { ListTaskCategory, TaskResponse } from "../../../api/workflow.ts";
import { Container, FormControlLabel, FormGroup, Switch, Typography } from "@mui/material";
import { useTranslation } from "react-i18next";
import PageHeader from "../PageHeader.tsx";
import { getTasks } from "../../../api/api.ts";
import { useAppDispatch } from "../../../redux/hooks.ts";
import TaskCard from "./TaskCard.tsx";
import PageContainer, { PageEmpty } from "../PageContainer.tsx";

const defaultPageSize = 25;
const autoRefreshInterval = 20 * 1000;

const DownloadList = () => {
  const { t } = useTranslation();
  const dispatch = useAppDispatch();

  const [nextPageToken, setNextPageToken] = useState<string | undefined>("");
  const [tasks, setTasks] = useState<TaskResponse[]>([]);
  const [downloadingTasks, setDownloadingTasks] = useState<TaskResponse[] | undefined>(undefined);
  const [loading, setLoading] = useState(false);
  const [autoRefresh, setAutoRefresh] = useState(true);
  const interval = React.useRef<NodeJS.Timeout>();
  const downloadingListHash = useRef("");

  useEffect(() => {
    if (autoRefresh && !interval.current) {
      interval.current = setInterval(() => {
        refresh();
      }, autoRefreshInterval);
    } else {
      clearInterval(interval.current);
      interval.current = undefined;
    }
  }, [autoRefresh]);

  useEffect(() => {
    return () => {
      clearInterval(interval.current);
      interval.current = undefined;
    };
  }, []);

  const loadNextPage = useCallback(
    (originTasks: TaskResponse[], token?: string) => () => {
      setLoading(true);
      dispatch(
        getTasks({
          page_size: defaultPageSize,
          category: ListTaskCategory.downloaded,
          next_page_token: token,
        }),
      )
        .then((res) => {
          if (token) {
            setAutoRefresh(false);
          }
          setTasks([...originTasks, ...res.tasks]);
          if (res.pagination?.next_token) {
            setNextPageToken(res.pagination.next_token);
          } else {
            setNextPageToken(undefined);
          }
        })
        .catch(() => {
          setNextPageToken(undefined);
        })
        .finally(() => {
          setLoading(false);
        });
    },
    [dispatch, setAutoRefresh, setTasks],
  );

  const loadDownloading = useCallback(() => {
    setLoading(true);
    dispatch(
      getTasks({
        page_size: defaultPageSize,
        category: ListTaskCategory.downloading,
      }),
    )
      .then((res) => {
        setDownloadingTasks(res.tasks);
        // New hash = id of first downloading task + id of last downloading task + length of downloading tasks
        const newHash = `${res.tasks[0]?.id ?? ""}-${res.tasks[res.tasks.length - 1]?.id ?? ""}-${res.tasks.length}`;

        if (downloadingListHash.current != "" && downloadingListHash.current != newHash) {
          loadNextPage([], "")();
        }
        downloadingListHash.current = newHash;
      })
      .catch(() => {})
      .finally(() => {
        setLoading(false);
      });
  }, [dispatch, setAutoRefresh, setDownloadingTasks]);

  const refresh = () => {
    loadDownloading();
  };

  const toggleAutoRefresh = (event: React.ChangeEvent<HTMLInputElement>) => {
    if (event.target.checked && tasks.length > defaultPageSize) {
      loadNextPage([], "")();
    }
    setAutoRefresh(event.target.checked);
  };

  return (
    <PageContainer compact>
      <Container maxWidth="lg">
        <PageHeader
          secondaryAction={
            <FormGroup>
              <FormControlLabel
                sx={{ mx: 0 }}
                control={<Switch size={"small"} onChange={toggleAutoRefresh} checked={autoRefresh} />}
                label={
                  <Typography variant={"body2"} color={"text.secondary"}>
                    {t("setting.autoRefresh")}
                  </Typography>
                }
              />
            </FormGroup>
          }
          onRefresh={refresh}
          loading={loading}
          title={t("application:navbar.remoteDownload")}
        />
        <Typography variant="caption" component="h2" sx={{ mb: 1 }} color="text.secondary" fontWeight={600}>
          {t("download.active")}
        </Typography>
        {downloadingTasks != undefined && downloadingTasks.length == 0 && (
          <PageEmpty>{t("setting.listEmpty")}</PageEmpty>
        )}
        {downloadingTasks == undefined && <TaskCard onLoad={loadDownloading} loading={true} />}

        {downloadingTasks && downloadingTasks.map((task) => <TaskCard showProgress key={task.id} task={task} />)}
        <Typography variant="caption" component="h2" sx={{ mb: 1, mt: 2 }} color="text.secondary" fontWeight={600}>
          {t("download.finished")}
        </Typography>
        {tasks.map((task) => (
          <TaskCard key={task.id} task={task} />
        ))}
        {nextPageToken != undefined && (
          <TaskCard onLoad={loadNextPage(tasks, nextPageToken)} loading={true} key={nextPageToken} />
        )}

        {nextPageToken == undefined && tasks.length == 0 && (
          <PageEmpty>{t("setting.listEmpty")}</PageEmpty>
        )}
      </Container>
    </PageContainer>
  );
};

export default DownloadList;
