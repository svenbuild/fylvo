import {
  Card,
  CardActionArea,
  CardContent,
  CardMedia,
  DialogActions,
  DialogContent,
  Grid2,
  styled,
  Typography,
} from "@mui/material";
import { hoverSurface } from "../../../fylvoTheme.ts";
import { useTranslation } from "react-i18next";
import { PolicyType } from "../../../api/explorer";
import { SecondaryButton } from "../../Common/StyledComponents";
import DraggableDialog from "../../Dialogs/DraggableDialog";
import Open from "../../Icons/Open";
import { PolicyPropsMap } from "./StoragePolicySetting";
import { useState } from "react";
import ProDialog from "../Common/ProDialog.tsx";
import { ProChip } from "../../Pages/Setting/SettingForm.tsx";

export interface SelectProviderProps {
  open: boolean;
  onClose: () => void;
  onSelect: (provider: PolicyType) => void;
}

const StyledCard = styled(Card)(({ theme }) => ({
  display: "flex",
  "&:hover": { backgroundColor: hoverSurface(theme) },
  "&:focus-within": { borderColor: theme.palette.primary.main },
}));

const SelectProvider = ({ open, onClose, onSelect }: SelectProviderProps) => {
  const { t } = useTranslation("dashboard");
  const [proOpen, setProOpen] = useState(false);
  return (
    <DraggableDialog
      title={t("policy.selectAStorageProvider")}
      dialogProps={{
        open,
        onClose,
        fullWidth: true,
        maxWidth: "sm",
      }}
    >
      <ProDialog open={proOpen} onClose={() => setProOpen(false)} />
      <DialogContent dividers>
        <Grid2 container spacing={1} sx={{ py: 1 }}>
          {Object.values(PolicyType).map((type) => (
            <Grid2 key={type.toString()} size={{ xs: 12, sm: 6 }}>
              <StyledCard>
                <CardActionArea
                  sx={{ display: "flex", justifyContent: "flex-start", minHeight: 40, px: 1, gap: 1 }}
                  onClick={() => (PolicyPropsMap[type].pro ? setProOpen(true) : onSelect(type))}
                >
                  <CardMedia
                    component="img"
                    image={PolicyPropsMap[type].img}
                    sx={{ width: 44, height: 32, objectFit: "contain" }}
                  />
                  <CardContent sx={{ p: 0, "&:last-child": { pb: 0 } }}>
                    <Typography variant="body2" color="text.primary">
                      {t(PolicyPropsMap[type].name)}
                      {PolicyPropsMap[type].pro && (
                        <ProChip
                          sx={{ background: "none", color: "text.secondary", border: 1, borderColor: "divider" }}
                          size="small"
                          label="Pro"
                        />
                      )}
                    </Typography>
                  </CardContent>
                </CardActionArea>
              </StyledCard>
            </Grid2>
          ))}
        </Grid2>
      </DialogContent>
      <DialogActions sx={{ justifyContent: "flex-start" }}>
        <SecondaryButton
          startIcon={<Open />}
          variant="contained"
          onClick={() => window.open("https://docs.cloudreve.org/usage/storage/", "_blank")}
        >
          {t("policy.compare")}
        </SecondaryButton>
      </DialogActions>
    </DraggableDialog>
  );
};

export default SelectProvider;
