import { Box, Fade, SvgIconProps } from "@mui/material";
import { TransitionGroup } from "react-transition-group";
import "../../Common/FadeTransition.css";
import SvgIcon from "@mui/material/SvgIcon/SvgIcon";

export interface NavIconTransitionProps {
  fileIcon: ((props: SvgIconProps) => JSX.Element)[] | (typeof SvgIcon)[];
  active?: boolean;
  [key: string]: any;
  iconProps?: SvgIconProps;
}

const NavIconTransition = ({ fileIcon, active, iconProps, ...rest }: NavIconTransitionProps) => {
  const [Active, InActive] = fileIcon;
  const Icon = active ? Active : InActive;
  return (
    <Box {...rest}>
      <TransitionGroup style={{ display: "grid", placeItems: "center", height: "100%" }}>
        <Fade key={active ? "active" : "inactive"}>
          <span style={{ gridArea: "1 / 1", display: "flex" }}>
            <Icon {...iconProps} />
          </span>
        </Fade>
      </TransitionGroup>
    </Box>
  );
};

export default NavIconTransition;
