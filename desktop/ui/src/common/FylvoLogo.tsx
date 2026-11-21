import { Box } from "@mui/material";
import { useTheme } from "@mui/material/styles";
import logoDark from "../assets/logo.svg";
import logoLight from "../assets/logo_light.svg";

interface FylvoLogoProps {
  height?: number;
}

export default function FylvoLogo({ height = 28 }: FylvoLogoProps) {
  const theme = useTheme();
  const logo = theme.palette.mode === "dark" ? logoLight : logoDark;

  return (
    <Box
      component="img"
      src={logo}
      alt="Fylvo"
      sx={{ height }}
    />
  );
}
