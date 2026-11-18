import { BoxProps, Typography, useMediaQuery, useTheme } from "@mui/material";
import { hoverSurface } from "../../fylvoTheme.ts";
import { RadiusFrame } from "../Frame/RadiusFrame.tsx";

export const PageEmpty = ({ children }: { children: React.ReactNode }) => (
  <Typography
    variant="body2"
    color="text.secondary"
    sx={{ width: "100%", border: 1, borderColor: "divider", borderRadius: 1, p: 2.5 }}
  >
    {children}
  </Typography>
);

const PageContainer = ({ compact, contentWidth = 1000, ...props }: BoxProps & { compact?: boolean; contentWidth?: number }) => {
  const theme = useTheme();
  const isMobile = useMediaQuery(theme.breakpoints.down("sm"));
  return (
    <RadiusFrame
      {...props}
      sx={{
        flexGrow: 1,
        mb: isMobile ? 0 : 1,
        py: compact ? { xs: 2, sm: 2.5 } : 4,
        overflow: "auto",
        ...(compact && {
          "& > .MuiContainer-root": { maxWidth: contentWidth, px: { xs: 2, sm: 2.5 } },
          "& [data-page-header]": { mb: 2 },
          "& [data-page-header] > div": { flexWrap: "wrap", columnGap: 0.5, rowGap: 1, minHeight: 32 },
          "& [data-page-title]": { fontSize: 17, overflowWrap: "anywhere" },
          "& .MuiTabs-flexContainer": { gap: 3 },
          "& .MuiTab-root": {
            textTransform: "none",
            borderBottom: "1px solid transparent",
            "&.Mui-selected": { color: "text.primary", borderColor: "text.secondary", fontWeight: 600 },
            "&:hover::after, &.Mui-selected:hover::after": { bgcolor: hoverSurface(theme) },
            "&.Mui-focusVisible::after": { outline: `1px solid ${theme.palette.primary.main}` },
          },
          "& .MuiTabs-indicator": { display: "none" },
          "& .MuiButtonBase-root.Mui-focusVisible": { outline: `1px solid ${theme.palette.primary.main}`, outlineOffset: -2 },
          "@media (max-width: 599.95px), (pointer: coarse)": {
            "& .MuiButton-root, & .MuiIconButton-root, & .MuiTab-root, & .MuiOutlinedInput-root": {
              minHeight: 40,
            },
            "& .MuiIconButton-root": { minWidth: 40 },
          },
        }),
      }}
      square={isMobile}
      withBorder={!isMobile}
    />
  );
};

export default PageContainer;
