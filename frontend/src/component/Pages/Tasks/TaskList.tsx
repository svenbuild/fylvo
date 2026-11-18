import * as React from "react";
import { useCallback, useEffect, useState } from "react";
import { ListTaskCategory, TaskResponse } from "../../../api/workflow.ts";
import { Container, FormControlLabel, FormGroup, Switch, Typography } from "@mui/material";
import { useTranslation } from "react-i18next";
import PageHeader from "../PageHeader.tsx";
import { getTasks } from "../../../api/api.ts";
import { useAppDispatch, useAppSelector } from "../../../redux/hooks.ts";
import TaskCard from "./TaskCard.tsx";
import PageContainer, { PageEmpty } from "../PageContainer.tsx";

const defaultPageSize = 25;
const autoRefreshInterval = 20 * 1000;

const TaskList = () => {
  const { t } = useTranslation();
  const dispatch = useAppDispatch();
  const policyOption = useAppSelector((state) => state.globalState.policyOptionCache);
  const [nextPageToken, setNextPageToken] = useState<string | undefined>("");
  const [tasks, setTasks] = useState<TaskResponse[]>([]);
  const [loading, setLoading] = useState(false);
  const [autoRefresh, setAutoRefresh] = useState(true);
  const interval = React.useRef<NodeJS.Timeout>();

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
          category: ListTaskCategory.general,
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

  const refresh = () => {
    loadNextPage([], "")();
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
          title={t("application:navbar.taskQueue")}
        />
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

export default TaskList;
