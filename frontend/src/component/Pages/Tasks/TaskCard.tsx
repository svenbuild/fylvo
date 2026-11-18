import {
  Box,
  darken,
  lighten,
  Skeleton,
  styled,
  SvgIconProps,
  Theme,
  Typography,
  useMediaQuery,
  useTheme,
} from "@mui/material";
import MuiAccordion, { AccordionProps } from "@mui/material/Accordion";
import MuiAccordionDetails from "@mui/material/AccordionDetails";
import MuiAccordionSummary, { AccordionSummaryProps } from "@mui/material/AccordionSummary";
import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useInView } from "react-intersection-observer";
import { FileType } from "../../../api/explorer.ts";
import { hoverSurface, selectedSurface } from "../../../fylvoTheme.ts";
import { TaskResponse, TaskType } from "../../../api/workflow.ts";
import { useAppDispatch } from "../../../redux/hooks.ts";
import { DefaultButton } from "../../Common/StyledComponents.tsx";
import FileIcon from "../../FileManager/Explorer/FileIcon.tsx";
import Archive from "../../Icons/Archive.tsx";
import ArchiveArrow from "../../Icons/ArchiveArrow.tsx";
import ArrowImport from "../../Icons/ArrowImport.tsx";
import TaskDetail from "./TaskDetail.tsx";
import TaskSummaryStatus from "./TaskSummaryStatus.tsx";
import TaskSummaryTitle from "./TaskSummaryTitle.tsx";

const Accordion = styled((props: AccordionProps) => <MuiAccordion disableGutters elevation={0} square {...props} />)(({ theme }) => ({
  borderRadius: theme.shape.borderRadius,
  backgroundColor: theme.palette.background.paper,
  "&::before": { display: "none" },
  marginBottom: 4,
}));

const AccordionSummary = styled((props: AccordionSummaryProps) => <MuiAccordionSummary {...props} />)(() => ({
  flexDirection: "row-reverse",
  minHeight: 0,
  padding: 0,
  "& .MuiAccordionSummary-content": {
    margin: 0,
  },
}));

const AccordionDetails = styled(MuiAccordionDetails)(({ theme }) => ({
  padding: 14,
  borderTop: `1px solid ${theme.palette.divider}`,
  borderRadius: `0 0 ${theme.shape.borderRadius}px ${theme.shape.borderRadius}px`,
  backgroundColor: theme.palette.background.default,
}));

export const getProgressColor = (theme: Theme) =>
  theme.palette.mode === "dark" ? darken(theme.palette.primary.main, 0.4) : lighten(theme.palette.primary.main, 0.85);

export const SummaryButton = styled(DefaultButton)<{
  expanded: boolean;
  percentage?: number;
}>(({ theme, expanded, percentage }) => ({
  position: "relative",
  minHeight: 32,
  padding: "3px 9px",
  justifyContent: "flex-start",
  backgroundColor: expanded ? selectedSurface(theme) : "transparent",
  borderRadius: expanded
    ? `${theme.shape.borderRadius}px ${theme.shape.borderRadius}px 0 0`
    : theme.shape.borderRadius,
  "&:hover": { backgroundColor: hoverSurface(theme) },
  "&::after": {
    content: '""',
    position: "absolute",
    left: 0,
    bottom: 0,
    width: `${Math.min(percentage ?? 0, 100)}%`,
    height: 2,
    backgroundColor: theme.palette.primary.main,
    pointerEvents: "none",
  },
  "@media (max-width: 599.95px), (pointer: coarse)": { minHeight: 40 },
}));

export interface TaskCardProps {
  loading?: boolean;
  showProgress?: boolean;
  task?: TaskResponse;
  onLoad?: () => void;
}

const taskIconsMap: {
  [key: string]: (props: SvgIconProps) => JSX.Element;
} = {
  [TaskType.create_archive]: Archive,
  [TaskType.extract_archive]: ArchiveArrow,
  [TaskType.import]: ArrowImport,
};

const TaskCard = ({ loading, showProgress, onLoad, task }: TaskCardProps) => {
  const { t } = useTranslation();
  const theme = useTheme();
  const isMobile = useMediaQuery(theme.breakpoints.down("md"));
  const dispatch = useAppDispatch();
  const { ref, inView } = useInView({
    rootMargin: "200px 0px",
    triggerOnce: true,
    skip: !loading,
  });

  const [expanded, setExpanded] = useState(false);

  useEffect(() => {
    if (!inView) {
      return;
    }

    if (onLoad) {
      onLoad();
    }
  }, [inView]);

  const handleChange = (_event: React.SyntheticEvent, newExpanded: boolean) => {
    if (loading) {
      return;
    }
    setExpanded(newExpanded);
  };

  const TaskIcon = useMemo(() => {
    return taskIconsMap[task?.type ?? ""] ?? Archive;
  }, [task?.type]);

  return (
    <Accordion expanded={expanded} onChange={handleChange} TransitionProps={{ unmountOnExit: true }}>
      <AccordionSummary aria-controls="panel1d-content">
        <SummaryButton
          disabled={loading}
          expanded={expanded}
          fullWidth
          percentage={
            showProgress
              ? ((task?.summary?.props?.download?.downloaded ?? 0) * 100) /
                Math.max(task?.summary?.props?.download?.total ?? 0, 1)
              : undefined
          }
          startIcon={
            loading ? (
              <Skeleton ref={loading ? ref : undefined} variant={"rounded"} width={16} height={16} />
            ) : task?.type === TaskType.remote_download ? (
              <FileIcon
                sx={{
                  p: 0,
                  height: 18,
                }}
                file={{
                  type: (task?.summary?.props.download?.files?.length ?? 0) > 1 ? FileType.folder : FileType.file,
                  name: task?.summary?.props.download?.name ?? "",
                }}
              />
            ) : (
              <TaskIcon />
            )
          }
        >
          <Box
            sx={{
              display: "flex",
              width: "100%",
              textAlign: "left",
              justifyContent: "space-between",
              alignItems: "center",
              gap: 1,
              minWidth: 0,
            }}
          >
            <Typography
              variant={"inherit"}
              sx={{
                flexGrow: 1,
                minWidth: 0,
                overflowWrap: "anywhere",
                "& .MuiButton-root.MuiButton-outlined": { minHeight: 24, py: 0, px: 0.5 },
              }}
            >
              {loading || !task ? (
                <Skeleton variant={"text"} width={150} />
              ) : (
                <Box component="span">
                  <TaskSummaryTitle type={task.type} summary={task.summary} />
                </Box>
              )}
            </Typography>

            <Typography
              color="text.secondary"
              variant="inherit"
              sx={{
                display: "flex",
                alignItems: "center",
                "& .MuiTypography-root": {
                  typography: "caption",
                  alignItems: "center",
                  gap: 0.5,
                  mx: 0,
                  "& > .MuiBox-root": { mt: 0, pt: 0, mr: 0, display: "inline-flex" },
                },
              }}
            >
              {loading || !task ? (
                <Skeleton variant={"text"} width={50} />
              ) : (
                <TaskSummaryStatus
                  simplified={isMobile}
                  type={task.type}
                  status={task.status}
                  error={task.error}
                  summary={task.summary}
                />
              )}
            </Typography>
          </Box>
        </SummaryButton>
      </AccordionSummary>
      <AccordionDetails>{task && <TaskDetail task={task} downloading={showProgress} />}</AccordionDetails>
    </Accordion>
  );
};

export default TaskCard;
