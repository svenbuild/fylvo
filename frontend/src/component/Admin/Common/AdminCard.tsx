import { Box, styled } from "@mui/material";
import { hoverSurface } from "../../../fylvoTheme.ts";

export const BorderedCard = styled(Box)(({ theme }) => ({
  border: `1px solid ${theme.palette.divider}`,
  borderRadius: theme.shape.borderRadius,
  padding: theme.spacing(1.5),
  minWidth: 0,
  "& .MuiTypography-subtitle1": { fontSize: theme.typography.body2.fontSize },
  backgroundColor: theme.palette.background.paper,
}));

export const BorderedCardClickable = styled(BorderedCard)(({ theme }) => ({
  cursor: "pointer",
  "&:hover": {
    backgroundColor: hoverSurface(theme),
  },
}));
