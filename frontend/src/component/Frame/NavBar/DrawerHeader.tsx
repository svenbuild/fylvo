import { ChevronLeft } from "@mui/icons-material";
import { Box, IconButton, styled, useMediaQuery, useTheme } from "@mui/material";
import { useNavigate } from "react-router-dom";
import { setDrawerOpen } from "../../../redux/globalStateSlice.ts";
import { useAppDispatch } from "../../../redux/hooks.ts";
import Logo from "../../Common/Logo.tsx";

export const DrawerHeaderContainer = styled("div")(({ theme }) => ({
  display: "flex",
  alignItems: "center",
  padding: "0 8px 0 12px",
  minHeight: 44,
  flexShrink: 0,
  [theme.breakpoints.down("sm")]: { minHeight: 48 },
  "@media (pointer: coarse)": { minHeight: 48 },
  justifyContent: "flex-end",
}));

const DrawerHeader = ({ disabled }: { disabled?: boolean }) => {
  const theme = useTheme();
  const isMobile = useMediaQuery(theme.breakpoints.down("sm"));
  const dispatch = useAppDispatch();
  const navigate = useNavigate();

  return (
    <DrawerHeaderContainer
      sx={{
        "& .MuiIconButton-root": { opacity: 0 },
        ...(!disabled && {
          "&:hover .MuiIconButton-root, & .MuiIconButton-root.Mui-focusVisible": { opacity: 1 },
          "@media (pointer: coarse)": { "& .MuiIconButton-root": { opacity: 1, minWidth: 40, minHeight: 40 } },
        }),
      }}
    >
      <Box sx={{ flexGrow: 1, minWidth: 0, cursor: "pointer" }} onClick={() => navigate("/home")}>
        <Logo
          sx={{
            height: "auto",
            maxWidth: 108,
            maxHeight: 24,
            width: "100%",
            objectPosition: "left",
            objectFit: "contain",
          }}
        />
      </Box>
      {!isMobile && (
        <IconButton
          aria-label="close drawer"
          onClick={() => dispatch(setDrawerOpen(false))}
          sx={{
            color: "text.secondary",
            "&.Mui-focusVisible": { outline: "1px solid", outlineColor: "primary.main", outlineOffset: -1 },
          }}
        >
          <ChevronLeft />
        </IconButton>
      )}
    </DrawerHeaderContainer>
  );
};

export default DrawerHeader;
