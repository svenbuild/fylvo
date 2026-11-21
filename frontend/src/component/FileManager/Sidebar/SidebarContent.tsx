import { Box } from "@mui/material";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { FileResponse } from "../../../api/explorer.ts";
import useActionDisplayOpt from "../ContextMenu/useActionDisplayOpt.ts";
import { StyledTab, StyledTabs } from "../../Common/StyledComponents.tsx";
import Details from "./Details.tsx";
import FileActivity, { hasFileActivity } from "./FileActivity.tsx";
import Header from "./Header.tsx";

export interface SidebarContentProps {
  target: FileResponse | undefined | null;
  inPhotoViewer?: boolean;
  setTarget: (target: FileResponse | undefined | null) => void;
}

interface TabPanelProps {
  children?: React.ReactNode;
  index: number;
  value: number;
}

function TabPanel(props: TabPanelProps) {
  const { children, value, index, ...other } = props;

  return (
    <div
      role="tabpanel"
      hidden={value !== index}
      id={`full-width-tabpanel-${index}`}
      aria-labelledby={`full-width-tab-${index}`}
      {...other}
    >
      {value === index && <Box sx={{ px: 2, py: 1.5 }}>{children}</Box>}
    </div>
  );
}

const SidebarContent = ({ target, inPhotoViewer, setTarget }: SidebarContentProps) => {
  const { t } = useTranslation();
  const targetDisplayOptions = useActionDisplayOpt(target ? [target] : []);
  // Fylvo: a second tab with the history, for the owner.
  const [tab, setTab] = useState(0);
  const withActivity = !!target && !inPhotoViewer && hasFileActivity(target);
  useEffect(() => {
    if (!withActivity) {
      setTab(0);
    }
  }, [withActivity]);
  return (
    <Box sx={{ display: "flex", flexDirection: "column", height: "100%" }}>
      <Header target={target} />
      {target != null && (
        <Box sx={{ overflow: "auto", minHeight: 0 }}>
          {withActivity && (
            <Box sx={{ px: 2, borderBottom: 1, borderColor: "divider" }}>
              <StyledTabs value={tab} onChange={(_e, v) => setTab(v)}>
                <StyledTab label={t("application:activity.details")} />
                <StyledTab label={t("application:activity.title")} />
              </StyledTabs>
            </Box>
          )}
          <TabPanel value={tab} index={0}>
            <Details
              inPhotoViewer={inPhotoViewer}
              target={target}
              setTarget={setTarget}
              targetDisplayOptions={targetDisplayOptions}
            />
          </TabPanel>
          {withActivity && (
            <TabPanel value={tab} index={1}>
              <FileActivity target={target} />
            </TabPanel>
          )}
        </Box>
      )}
    </Box>
  );
};

export default SidebarContent;
