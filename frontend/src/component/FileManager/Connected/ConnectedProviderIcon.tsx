import { SvgIconProps } from "@mui/material";
import { ConnectedProvider } from "../../../api/connected.ts";
import CloudFilled from "../../Icons/CloudFilled.tsx";
import CloudOutlined from "../../Icons/CloudOutlined.tsx";
import GoogleDrive from "../../Icons/GoogleDrive.tsx";

type Icon = (props: SvgIconProps) => JSX.Element;

// Fylvo: the picture a connected storage is shown with, as a pair for "selected" and "not
// selected": the provider's own logo where there is one, a cloud otherwise. A logo keeps its
// colours, so it is the same in both.
export const connectedProviderIcons = (provider?: string): [Icon, Icon] =>
  provider == ConnectedProvider.drive ? [GoogleDrive, GoogleDrive] : [CloudFilled, CloudOutlined];

const ConnectedProviderIcon = ({ provider, ...props }: SvgIconProps & { provider?: string }) => {
  const Icon = connectedProviderIcons(provider)[1];
  return <Icon {...props} />;
};

export default ConnectedProviderIcon;
