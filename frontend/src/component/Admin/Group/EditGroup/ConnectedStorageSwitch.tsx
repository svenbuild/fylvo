import { FormControl, FormControlLabel, Switch } from "@mui/material";
import { useContext } from "react";
import { useTranslation } from "react-i18next";
import { GroupEnt } from "../../../../api/dashboard";
import { GroupPermission } from "../../../../api/user";
import Boolset from "../../../../util/boolset";
import SettingForm from "../../../Pages/Setting/SettingForm";
import { NoMarginHelperText } from "../../Settings/Settings";
import { GroupSettingContext } from "./GroupSettingWrapper";

// Fylvo: group permission to connect storages at other providers.
const ConnectedStorageSwitch = () => {
  const { t } = useTranslation("dashboard");
  const { values, setGroup } = useContext(GroupSettingContext);
  const enabled = new Boolset(values.permissions ?? "").enabled(GroupPermission.connected_storage);

  const onChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const checked = e.target.checked;
    setGroup((p: GroupEnt) => ({
      ...p,
      permissions: new Boolset(p.permissions).set(GroupPermission.connected_storage, checked).toString(),
    }));
  };

  return (
    <SettingForm lgWidth={5}>
      <FormControl fullWidth>
        <FormControlLabel control={<Switch checked={enabled} onChange={onChange} />} label={t("connected.allow")} />
        <NoMarginHelperText>{t("connected.allowDes")}</NoMarginHelperText>
      </FormControl>
    </SettingForm>
  );
};

export default ConnectedStorageSwitch;
