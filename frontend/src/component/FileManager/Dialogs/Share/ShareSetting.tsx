import {
  Box,
  IconButton,
  InputAdornment,
  MenuItem,
  styled,
  Switch,
  TextField,
  ToggleButton,
  ToggleButtonGroup,
  Tooltip,
  Typography,
} from "@mui/material";
import { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { FileResponse, FileType, ShareMember } from "../../../../api/explorer.ts";
import { sizeToString } from "../../../../util";
import { DenseSelect } from "../../../Common/StyledComponents.tsx";
import ArrowShuffle from "../../../Icons/ArrowShuffle.tsx";
import LinkOutlined from "../../../Icons/LinkOutlined.tsx";
import People from "../../../Icons/People.tsx";
import ShareMembers from "./ShareMembers.tsx";

export interface ShareSetting {
  is_private?: boolean;
  password?: string;
  share_view?: boolean;
  show_readme?: boolean;
  downloads?: boolean;
  expires?: boolean;
  // Fylvo: share with named people instead of a link. Fixed once the share exists.
  member_only?: boolean;
  members?: ShareMember[];
  // Fylvo: visitors of a folder link may upload; hide_content makes it "upload only".
  allow_upload?: boolean;
  hide_content?: boolean;
  upload_limit?: boolean;

  // Seconds until the link expires, number of downloads, upload limit in bytes.
  expires_val: number;
  downloads_val: number;
  upload_limit_val: number;
}

export interface ShareSettingProps {
  setting: ShareSetting;
  file?: FileResponse;
  onSettingChange: (value: ShareSetting) => void;
  editing?: boolean;
}

const GB = 1024 * 1024 * 1024;

// Fylvo: the values are picked from fixed lists, so nothing but these numbers can be entered.
// 0 in a list stands for "off" (never expires, no limit).
export const expireOptions = [300, 3600, 24 * 3600, 7 * 24 * 3600, 30 * 24 * 3600];
export const downloadOptions = [1, 2, 3, 5, 10, 20, 50, 100];
export const uploadLimitOptions = [1, 5, 20, 100].map((n) => n * GB);

// An existing share can hold a value that is not in the list. It is offered as well, so editing keeps it.
const withCurrent = (options: number[], current: number) =>
  options.includes(current) || !(current > 0) ? options : [...options, current].sort((a, b) => a - b);

// A random password like the server makes one: 8 lower case letters and digits.
const randomPassword = () => {
  const chars = "abcdefghijklmnopqrstuvwxyz0123456789";
  return Array.from(crypto.getRandomValues(new Uint32Array(8)), (n) => chars[n % chars.length]).join("");
};

const controlWidth = 172;

const SectionLabel = ({ children }: { children: ReactNode }) => (
  <Typography
    variant="caption"
    color="text.secondary"
    sx={{ display: "block", fontWeight: 600, px: 0.5, pt: 1.5, pb: 0.5, "&:first-of-type": { pt: 0.5 } }}
  >
    {children}
  </Typography>
);

// Every option is one row: the name on the left, one control on the right. Rows never change height.
const Row = styled("label")(({ theme }) => ({
  display: "flex",
  alignItems: "center",
  gap: 12,
  minHeight: 36,
  padding: "0 4px",
  "& > .MuiTypography-root": { flexGrow: 1, minWidth: 0 },
  "& > :last-child": { flexShrink: 0 },
  "@media (max-width: 599.95px), (pointer: coarse)": { minHeight: 44 },
  "&[aria-disabled=true] > .MuiTypography-root": { color: theme.palette.text.disabled },
  // A disabled field must look quieter than an active one, not brighter.
  "& .Mui-disabled .MuiOutlinedInput-notchedOutline": { borderColor: theme.palette.divider },
}));

const OptionRow = ({ label, disabled, children }: { label: string; disabled?: boolean; children: ReactNode }) => (
  <Row aria-disabled={disabled}>
    <Typography variant="body2">{label}</Typography>
    {children}
  </Row>
);

interface ValueSelectProps {
  // 0 is "off".
  value: number;
  options: number[];
  offLabel: string;
  format: (value: number) => string;
  onChange: (value: number) => void;
  disabled?: boolean;
}

const ValueSelect = ({ value, options, offLabel, format, onChange, disabled }: ValueSelectProps) => (
  <DenseSelect
    variant="outlined"
    size="small"
    value={value}
    disabled={disabled}
    onChange={(e) => onChange(e.target.value as number)}
    sx={{ width: controlWidth, fontSize: (theme) => theme.typography.body2.fontSize }}
  >
    <MenuItem dense value={0}>
      {offLabel}
    </MenuItem>
    {options.map((option) => (
      <MenuItem dense key={option} value={option}>
        {format(option)}
      </MenuItem>
    ))}
  </DenseSelect>
);

const ShareSettingContent = ({ setting, file, editing, onSettingChange }: ShareSettingProps) => {
  const { t, i18n } = useTranslation();
  const isFolder = file?.type == FileType.folder;
  const isLink = !setting.member_only;

  const change = (patch: Partial<ShareSetting>) => onSettingChange({ ...setting, ...patch });

  const formatExpiry = (seconds: number) => {
    if (seconds % (24 * 3600) == 0) return t("application:modals.durationDays", { count: seconds / (24 * 3600) });
    if (seconds % 3600 == 0) return t("application:modals.durationHours", { count: seconds / 3600 });
    if (seconds % 60 == 0) return t("application:modals.durationMinutes", { count: seconds / 60 });
    // The rest of an existing share's time: show when it ends.
    const end = new Date(Date.now() + seconds * 1000);
    return t("application:modals.expiresUntil", {
      date: end.toLocaleString(i18n.language, { dateStyle: "short", timeStyle: "short" }),
    });
  };

  const setPassword = (password: string) => change({ password, is_private: password != "" });

  return (
    <Box>
      {!editing && isFolder && (
        <ToggleButtonGroup
          value={setting.member_only ? "people" : "link"}
          exclusive
          fullWidth
          size="small"
          sx={{ mb: 1 }}
          onChange={(_e, mode) => mode && change({ member_only: mode == "people" })}
        >
          <ToggleButton value="link" sx={{ textTransform: "none" }}>
            <LinkOutlined fontSize="small" sx={{ mr: 0.5 }} />
            <Typography variant="caption">{t("application:modals.shareModeLink")}</Typography>
          </ToggleButton>
          <ToggleButton value="people" sx={{ textTransform: "none" }}>
            <People fontSize="small" sx={{ mr: 0.5 }} />
            <Typography variant="caption">{t("application:modals.shareModePeople")}</Typography>
          </ToggleButton>
        </ToggleButtonGroup>
      )}
      {setting.member_only && (
        <ShareMembers members={setting.members ?? []} onChange={(members) => change({ members })} />
      )}

      {/* A file has only this one group, so it needs no heading. */}
      {isFolder && <SectionLabel>{t("application:modals.sectionAccess")}</SectionLabel>}
      {isLink && (
        <OptionRow label={t("application:modals.passwordOption")} disabled={editing}>
          <TextField
            size="small"
            disabled={editing}
            placeholder={t("application:modals.noPassword")}
            value={setting.is_private ? setting.password ?? "" : ""}
            onChange={(e) => {
              const value = e.target.value.trim();
              if (!/^[a-zA-Z0-9]*$/.test(value) || value.length > 32) return;
              setPassword(value);
            }}
            slotProps={{
              htmlInput: { maxLength: 32 },
              input: {
                endAdornment: !editing && (
                  <InputAdornment position="end" sx={{ mr: -1 }}>
                    <Tooltip title={t("application:modals.randomPassword")}>
                      <IconButton size="small" onClick={() => setPassword(randomPassword())}>
                        <ArrowShuffle sx={{ fontSize: 16 }} />
                      </IconButton>
                    </Tooltip>
                  </InputAdornment>
                ),
              },
            }}
            sx={{ width: controlWidth, "& input": { fontSize: (theme) => theme.typography.body2.fontSize } }}
          />
        </OptionRow>
      )}
      <OptionRow label={t("application:modals.expiresOption")}>
        <ValueSelect
          value={setting.expires ? setting.expires_val : 0}
          options={withCurrent(expireOptions, setting.expires_val)}
          offLabel={t("application:modals.unlimited")}
          format={formatExpiry}
          onChange={(seconds) => change(seconds ? { expires: true, expires_val: seconds } : { expires: false })}
        />
      </OptionRow>
      {file?.type == FileType.file && (
        <OptionRow label={t("application:modals.downloadsOption")}>
          <ValueSelect
            value={setting.downloads ? setting.downloads_val : 0}
            options={withCurrent(downloadOptions, setting.downloads_val)}
            offLabel={t("application:modals.unlimited")}
            format={(num) => t("application:modals.downloadLimitOptions", { num, count: num })}
            onChange={(num) => change(num ? { downloads: true, downloads_val: num } : { downloads: false })}
          />
        </OptionRow>
      )}

      {isFolder && isLink && (
        <>
          <SectionLabel>{t("application:modals.sectionUploads")}</SectionLabel>
          <OptionRow label={t("application:modals.allowUploadOption")}>
            <DenseSelect
              variant="outlined"
              size="small"
              value={!setting.allow_upload ? "off" : setting.hide_content ? "upload_only" : "view_upload"}
              onChange={(e) =>
                change({ allow_upload: e.target.value != "off", hide_content: e.target.value == "upload_only" })
              }
              sx={{ width: controlWidth, fontSize: (theme) => theme.typography.body2.fontSize }}
            >
              <MenuItem dense value="off">
                {t("application:modals.uploadsOff")}
              </MenuItem>
              <MenuItem dense value="view_upload">
                {t("application:modals.viewAndUpload")}
              </MenuItem>
              <MenuItem dense value="upload_only">
                {t("application:modals.uploadOnly")}
              </MenuItem>
            </DenseSelect>
          </OptionRow>
          <OptionRow label={t("application:modals.uploadLimitOption")} disabled={!setting.allow_upload}>
            <ValueSelect
              value={setting.upload_limit ? setting.upload_limit_val : 0}
              options={withCurrent(uploadLimitOptions, setting.upload_limit_val)}
              offLabel={t("application:modals.unlimited")}
              format={sizeToString}
              disabled={!setting.allow_upload}
              onChange={(bytes) =>
                change(bytes ? { upload_limit: true, upload_limit_val: bytes } : { upload_limit: false })
              }
            />
          </OptionRow>
        </>
      )}

      {isFolder && (
        <>
          <SectionLabel>{t("application:modals.sectionView")}</SectionLabel>
          <OptionRow label={t("application:modals.shareViewOption")}>
            <Switch
              size="small"
              checked={!!setting.share_view}
              onChange={() => change({ share_view: !setting.share_view })}
            />
          </OptionRow>
          <OptionRow label={t("application:modals.showReadmeOption")}>
            <Switch
              size="small"
              checked={!!setting.show_readme}
              onChange={() => change({ show_readme: !setting.show_readme })}
            />
          </OptionRow>
        </>
      )}
    </Box>
  );
};

export default ShareSettingContent;
