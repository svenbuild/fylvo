import { FormControlLabel, Switch } from "@mui/material";
import { useTranslation } from "react-i18next";
import { NoMarginHelperText } from "../Settings/Settings";

// Fylvo: the person has to set their own password at the next sign-in.
interface MustChangePasswordSwitchProps {
  checked: boolean;
  onChange: (checked: boolean) => void;
}

const MustChangePasswordSwitch = ({ checked, onChange }: MustChangePasswordSwitchProps) => {
  const { t } = useTranslation("dashboard");
  return (
    <>
      <FormControlLabel
        sx={{ mt: 1 }}
        control={<Switch checked={checked} onChange={(e) => onChange(e.target.checked)} />}
        label={t("user.mustChangePassword")}
      />
      <NoMarginHelperText>{t("user.mustChangePasswordDes")}</NoMarginHelperText>
    </>
  );
};

export default MustChangePasswordSwitch;
