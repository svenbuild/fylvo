import { FormControl, Typography } from "@mui/material";
import { useTranslation } from "react-i18next";
import { OutlineIconTextField } from "../../../Common/Form/OutlineIconTextField.tsx";
import Password from "../../../Icons/Password.tsx";
import { Control } from "../Signin/SignIn.tsx";

// Fylvo: the account has to set its own password before the first sign-in completes.
interface PhaseSetNewPasswordProps {
  password: string;
  setPassword: (password: string) => void;
  repeat: string;
  setRepeat: (repeat: string) => void;
  control?: Control;
}

const PhaseSetNewPassword = ({ password, setPassword, repeat, setRepeat, control }: PhaseSetNewPasswordProps) => {
  const { t } = useTranslation();
  return (
    <>
      <Typography color={"text.secondary"}>{t("login.setNewPasswordHint")}</Typography>
      <FormControl variant="standard" margin="normal" required fullWidth>
        <OutlineIconTextField
          autoFocus
          variant={"outlined"}
          label={t("login.newPassword")}
          inputProps={{
            name: "new-password",
            type: "password",
            id: "new-password",
            required: true,
            minLength: 6,
            maxLength: 128,
          }}
          onChange={(e) => setPassword(e.target.value)}
          icon={<Password />}
          value={password}
          autoComplete={"new-password"}
        />
      </FormControl>
      <FormControl variant="standard" margin="normal" required fullWidth>
        <OutlineIconTextField
          variant={"outlined"}
          label={t("login.repeatNewPassword")}
          inputProps={{
            name: "repeat-new-password",
            type: "password",
            id: "repeat-new-password",
            required: true,
            minLength: 6,
            maxLength: 128,
          }}
          onChange={(e) => setRepeat(e.target.value)}
          icon={<Password />}
          value={repeat}
          autoComplete={"new-password"}
        />
      </FormControl>
      {control?.submit}
      {control?.back}
    </>
  );
};

export default PhaseSetNewPassword;
