import { Box, BoxProps, Typography, useTheme } from "@mui/material";
import LogoIcon from "./assets/logo.svg";
import LogoIconDark from "./assets/logo_light.svg";

export interface PoweredByProps extends BoxProps {}

const PoweredBy = ({ ...rest }: PoweredByProps) => {
  const theme = useTheme();
  return (
    <Box {...rest}>
      <Box
        component="a"
        href="/"
        sx={{
          width: "100%",
          display: "flex",
          alignItems: "center",
          gap: 1,
          textDecoration: "none",
          "& img": {
            filter: "grayscale(100%)",
            opacity: 0.55,
          },
          "&:hover": {
            "& img": {
              filter: "grayscale(0%)",
              opacity: 1,
            },
          },
        }}
      >
        <Typography
          variant="caption"
          color="text.secondary"
        >
          Powered by
        </Typography>
        <Box
          component="img"
          alt="Fylvo"
          sx={{
            height: 14,
          }}
          src={theme.palette.mode === "dark" ? LogoIconDark : LogoIcon}
        />
      </Box>
    </Box>
  );
};

export default PoweredBy;
