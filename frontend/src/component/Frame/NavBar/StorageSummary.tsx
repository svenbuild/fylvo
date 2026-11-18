import { LinearProgress, linearProgressClasses, Skeleton, styled, Typography } from "@mui/material";
import { memo, useEffect } from "react";
import { useTranslation } from "react-i18next";
import { useAppDispatch, useAppSelector } from "../../../redux/hooks.ts";
import { updateUserCapacity } from "../../../redux/thunks/filemanager.ts";
import { sizeToString } from "../../../util";
import { RadiusFrame } from "../RadiusFrame.tsx";

const StyledBox = styled(RadiusFrame)(({ theme }) => ({
  padding: "6px 8px",
  backgroundColor: theme.palette.grey[theme.palette.mode === "light" ? 50 : 900],
  flexShrink: 0,
  "& .MuiTypography-caption": { lineHeight: 1.35 },
}));

const StorageHeaderContainer = styled("div")(() => ({
  display: "flex",
  justifyContent: "space-between",
  alignItems: "center",
}));

const BorderLinearProgress = styled(LinearProgress)<{ warning: boolean }>(({ theme, warning }) => ({
  height: 4,
  [`&.${linearProgressClasses.colorPrimary}`]: {
    backgroundColor: theme.palette.grey[theme.palette.mode === "light" ? 200 : 800],
  },
  [`& .${linearProgressClasses.bar}`]: {
    borderRadius: 4,
    backgroundColor: warning ? theme.palette.warning.main : theme.palette.primary.main,
  },
  marginBlock: "4px",
}));

const StorageSummary = memo(() => {
  const { t } = useTranslation("application");
  const dispatch = useAppDispatch();
  const capacity = useAppSelector((state) => state.fileManager[0].capacity);
  useEffect(() => {
    if (!capacity) {
      dispatch(updateUserCapacity(0));
      return;
    }
  }, [capacity]);
  return (
    <StyledBox withBorder>
      <StorageHeaderContainer>
        <Typography variant="caption" color="text.secondary" fontWeight={600}>
          {t("application:navbar.storage")}
        </Typography>
      </StorageHeaderContainer>
      {capacity && (
        <BorderLinearProgress
          warning={capacity.used > capacity.total}
          variant="determinate"
          value={Math.min(100, (capacity.used / capacity.total) * 100)}
        />
      )}
      {!capacity && <Skeleton sx={{ my: "4px", height: 4 }} variant="rounded" />}
      <Typography variant="caption" color="text.secondary" component="div" sx={{ fontVariantNumeric: "tabular-nums" }}>
        {capacity ? (
          `${sizeToString(capacity.used)} / ${sizeToString(capacity.total)}`
        ) : (
          <Skeleton sx={{ width: "50%" }} variant="text" />
        )}
      </Typography>
    </StyledBox>
  );
});

export default StorageSummary;
