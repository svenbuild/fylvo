import {
  FastForwardRounded,
  FastRewindRounded,
  PauseRounded,
  PlayArrowRounded,
  VolumeDownRounded,
  VolumeUpRounded,
} from "@mui/icons-material";
import {
  Box,
  IconButton,
  Popover,
  PopoverProps,
  Slider,
  Stack,
  styled,
  Typography,
  useMediaQuery,
  useTheme,
} from "@mui/material";
import { useEffect, useRef, useState } from "react";
import { FileResponse, Metadata } from "../../../api/explorer.ts";
import { useMediaSession } from "../../../hooks/useMediaSession";
import { useAppDispatch } from "../../../redux/hooks.ts";
import { loadFileThumb } from "../../../redux/thunks/file.ts";
import SessionManager, { UserSettings } from "../../../session";
import { FileManagerIndex } from "../../FileManager/FileManager.tsx";
import { MediaMetaElements } from "../../FileManager/Sidebar/MediaMetaCard.tsx";
import AppsList from "../../Icons/AppsList.tsx";
import ArrowRepeatAll from "../../Icons/ArrowRepeatAll.tsx";
import ArrowRepeatOne from "../../Icons/ArrowRepeatOne.tsx";
import ArrowRepeatOff from "../../Icons/ArrowRepeatOff.tsx";
import ArrowShuffle from "../../Icons/ArrowShuffle.tsx";
import MusicNote1 from "../../Icons/MusicNote1.tsx";
import { LoopMode } from "./MusicPlayer.tsx";
import Playlist from "./Playlist.tsx";
import RepeatModePopover from "./RepeatModePopover.tsx";

const WallPaper = styled("div")({
  position: "absolute",
  width: "100%",
  height: "100%",
  top: 0,
  left: 0,
  overflow: "hidden",
  background: "linear-gradient(rgb(255, 38, 142) 0%, rgb(255, 105, 79) 100%)",
  transition: "all 500ms cubic-bezier(0.175, 0.885, 0.32, 1.275) 0s",
  "&::before": {
    content: '""',
    width: "140%",
    height: "140%",
    position: "absolute",
    top: "-40%",
    right: "-50%",
    background: "radial-gradient(at center center, rgb(62, 79, 249) 0%, rgba(62, 79, 249, 0) 64%)",
  },
  "&::after": {
    content: '""',
    width: "140%",
    height: "140%",
    position: "absolute",
    bottom: "-50%",
    left: "-30%",
    background: "radial-gradient(at center center, rgb(247, 237, 225) 0%, rgba(247, 237, 225, 0) 70%)",
    transform: "rotate(30deg)",
  },
});

const PlayerPopover = styled(Popover)({
  "& .MuiPopover-paper": { boxShadow: "none" },
});

const Widget = styled("div")(({ theme }) => ({
  padding: "10px 12px",
  width: 300,
  maxWidth: "100%",
  margin: "auto",
  position: "relative",
  zIndex: 1,
  backgroundColor: theme.palette.background.paper,
  "& .MuiIconButton-root.Mui-focusVisible": { outline: `1px solid ${theme.palette.primary.main}` },
  "@media (max-width: 599.95px), (pointer: coarse)": {
    "& .MuiIconButton-root": { minWidth: 40, minHeight: 40 },
  },
}));

const CoverImage = styled("div")(({ theme }) => ({
  width: 36,
  height: 36,
  objectFit: "cover",
  overflow: "hidden",
  flexShrink: 0,
  backgroundColor: theme.palette.action.hover,
  color: theme.palette.text.secondary,
  borderRadius: 6,
  "& > img": {
    width: "100%",
  },
  display: "flex",
  alignItems: "center",
  justifyContent: "center",
}));

const TinyText = styled(Typography)({
  fontSize: "0.75rem",
  color: "inherit",
  opacity: 0.75,
  fontWeight: 500,
  letterSpacing: 0.2,
});

// Scrolling text component for long text
const ScrollingText = ({ children, text, ...props }: { children: React.ReactNode; text: string }) => {
  const containerRef = useRef<HTMLDivElement>(null);
  const contentRef = useRef<HTMLDivElement>(null);
  const [shouldScroll, setShouldScroll] = useState(false);
  const [animationDuration, setAnimationDuration] = useState(15);

  useEffect(() => {
    const checkOverflow = () => {
      if (containerRef.current && contentRef.current) {
        const isOverflowing = contentRef.current.scrollWidth > containerRef.current.clientWidth;
        setShouldScroll(isOverflowing);

        // Calculate animation duration based on text length
        if (isOverflowing) {
          const textLength = contentRef.current.scrollWidth;
          // Adjust speed based on text length (faster for longer text)
          const calculatedDuration = Math.max(5, Math.min(10, textLength / 15));
          setAnimationDuration(calculatedDuration);
        }
      }
    };

    setShouldScroll(false);
    setTimeout(() => {
      checkOverflow();
    }, 1000);
  }, [text]);

  return (
    <Box
      ref={containerRef}
      sx={{
        overflow: "hidden",
        whiteSpace: "nowrap",
        width: "100%",
        position: "relative",
      }}
      {...props}
    >
      {shouldScroll ? (
        <Box
          sx={{
            display: "flex",
            width: "100%",
            animation: `marquee ${animationDuration}s linear infinite`,
            "@keyframes marquee": {
              "0%": { transform: "translateX(0%)" },
              "100%": { transform: "translateX(-100%)" },
            },
          }}
        >
          <Box ref={contentRef} sx={{ whiteSpace: "nowrap", paddingRight: "50px" }}>
            {children}
          </Box>
          <Box sx={{ whiteSpace: "nowrap", paddingRight: "50px" }}>{children}</Box>
        </Box>
      ) : (
        <Box ref={contentRef}>{children}</Box>
      )}
    </Box>
  );
};

export interface PlayerPopupProps extends PopoverProps {
  file?: FileResponse;
  playlist?: FileResponse[];
  duration: number;
  current: number;
  onSeek: (time: number) => void;
  playing: boolean;
  togglePause: () => void;
  setVolumeLevel: (volume: number) => void;
  volume: number;
  loopProceed: (isNext: boolean) => void;
  manualNavigate: (isNext: boolean) => void;
  loopMode: number;
  toggleLoopMode: () => void;
  setLoopMode: (mode: number) => void;
  playbackSpeed: number;
  setPlaybackSpeed: (speed: number) => void;
  playIndex: (index: number, volume?: number) => void;
}

const isIOS = /iPad|iPhone/.test(navigator.userAgent);

export const PlayerPopup = ({
  file,
  duration,
  current,
  onSeek,
  playing,
  togglePause,
  volume,
  setVolumeLevel,
  loopMode,
  loopProceed,
  manualNavigate,
  toggleLoopMode,
  setLoopMode,
  playbackSpeed,
  setPlaybackSpeed,
  playlist,
  playIndex,
  ...rest
}: PlayerPopupProps) => {
  const theme = useTheme();
  const isMobile = useMediaQuery(theme.breakpoints.down("md"));
  const dispatch = useAppDispatch();
  const [thumbSrc, setThumbSrc] = useState<string | null>(null);
  const [thumbBgLoaded, setThumbBgLoaded] = useState(false);
  const [progress, setProgress] = useState(0);
  const seeking = useRef(false);
  const [anchorEl, setAnchorEl] = useState<null | HTMLElement>(null);
  const [repeatAnchorEl, setRepeatAnchorEl] = useState<null | HTMLElement>(null);

  function formatDuration(value: number) {
    const minute = Math.floor(value / 60);
    const secondLeft = value - minute * 60;
    return `${minute}:${secondLeft < 10 ? `0${secondLeft}` : secondLeft}`;
  }
  const mainIconColor = theme.palette.text.primary;
  const lightIconColor = theme.palette.text.secondary;

  useEffect(() => {
    setThumbBgLoaded(false);
    if (file && (!file.metadata || file.metadata[Metadata.thumbDisabled] === undefined)) {
      dispatch(loadFileThumb(FileManagerIndex.main, file)).then((src) => {
        setThumbSrc(src);
      });
    } else {
      setThumbSrc(null);
    }
  }, [file, dispatch]);

  useEffect(() => {
    if (seeking.current) {
      return;
    }
    setProgress(current);
  }, [current]);

  const onSeekCommit = (time: number) => {
    seeking.current = false;
    onSeek(time);
  };

  // Initialize Media Session API
  useMediaSession({
    file,
    playing,
    duration,
    current,
    thumbSrc,
    onPlay: togglePause,
    onPause: togglePause,
    onPrevious: () => manualNavigate(false),
    onNext: () => manualNavigate(true),
    onSeek,
  });

  return (
    <PlayerPopover
      anchorOrigin={{
        vertical: "bottom",
        horizontal: "left",
      }}
      transformOrigin={{
        vertical: "top",
        horizontal: "left",
      }}
      {...rest}
    >
      {playlist && file && (
        <Playlist
          playIndex={playIndex}
          anchorEl={anchorEl}
          open={Boolean(anchorEl)}
          onClose={() => setAnchorEl(null)}
          file={file}
          playlist={playlist}
        />
      )}
      <RepeatModePopover
        open={Boolean(repeatAnchorEl)}
        anchorEl={repeatAnchorEl}
        onClose={() => setRepeatAnchorEl(null)}
        loopMode={loopMode}
        onLoopModeChange={setLoopMode}
        playbackSpeed={playbackSpeed}
        onPlaybackSpeedChange={setPlaybackSpeed}
      />
      <Widget>
        <Box sx={{ display: "flex", alignItems: "center" }}>
          <CoverImage>
            {!thumbSrc && <MusicNote1 fontSize="small" />}
            {thumbSrc && <img src={thumbSrc} onError={() => setThumbSrc(null)} alt="cover" />}
          </CoverImage>
          <Box sx={{ ml: 1.5, minWidth: 0, maxWidth: "210px", width: "100%" }}>
            {file && file.metadata && file.metadata[Metadata.music_artist] && (
              <Typography variant="caption" color="text.secondary" fontWeight={500}>
                <MediaMetaElements
                  element={{
                    display: file.metadata[Metadata.music_artist],
                    searchValue: file.metadata[Metadata.music_artist],
                    searchKey: Metadata.music_artist,
                  }}
                />
              </Typography>
            )}
            {file && (
              <ScrollingText text={file.metadata?.[Metadata.music_title] ?? file.name}>
                <b>
                  {file.metadata?.[Metadata.music_title] ? (
                    <MediaMetaElements
                      element={{
                        display: file.metadata[Metadata.music_title],
                        searchValue: file.metadata[Metadata.music_title],
                        searchKey: Metadata.music_title,
                      }}
                    />
                  ) : (
                    file.name
                  )}
                </b>
              </ScrollingText>
            )}
            {file && file.metadata && file.metadata[Metadata.music_album] && (
              <ScrollingText text={file.metadata[Metadata.music_album]}>
                <Typography variant={"body2"} letterSpacing={-0.25}>
                  <MediaMetaElements
                    element={{
                      display: file.metadata[Metadata.music_album],
                      searchValue: file.metadata[Metadata.music_album],
                      searchKey: Metadata.music_album,
                    }}
                  />
                </Typography>
              </ScrollingText>
            )}
          </Box>
        </Box>
        <Slider
          aria-label="time-indicator"
          size="small"
          value={progress}
          onMouseDown={() => (seeking.current = true)}
          min={0}
          step={1}
          max={duration}
          onChange={(_, value) => setProgress(value as number)}
          onChangeCommitted={(_, value) => onSeekCommit(value as number)}
          sx={{
            color: "primary.main",
            height: 2,
            "& .MuiSlider-thumb": {
              width: 10,
              height: 10,
              "&::before, &:hover, &.Mui-active, &.Mui-focusVisible": { boxShadow: "none" },
              "&.Mui-focusVisible": { outline: "2px solid currentColor", outlineOffset: 2 },
            },
            "& .MuiSlider-rail": {
              opacity: 0.28,
            },
          }}
        />
        <Box
          sx={{
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
            mt: -2,
          }}
        >
          <TinyText>{formatDuration(current)}</TinyText>
          <TinyText>-{formatDuration(duration - current)}</TinyText>
        </Box>
        <Box
          sx={{
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
            mt: -1,
          }}
        >
          <IconButton aria-label="loop mode" onClick={(e) => setRepeatAnchorEl(e.currentTarget)}>
            {loopMode == LoopMode.list_repeat && <ArrowRepeatAll fontSize={"medium"} htmlColor={mainIconColor} />}
            {loopMode == LoopMode.single_repeat && <ArrowRepeatOne fontSize={"medium"} htmlColor={mainIconColor} />}
            {loopMode == LoopMode.shuffle && <ArrowShuffle fontSize={"medium"} htmlColor={mainIconColor} />}
            {loopMode == LoopMode.play_once && <ArrowRepeatOff fontSize={"medium"} htmlColor={mainIconColor} />}
          </IconButton>
          <Box
            sx={{
              display: "flex",
              alignItems: "center",
              justifyContent: "center",
            }}
          >
            <IconButton aria-label="previous song" onClick={() => manualNavigate(false)}>
              <FastRewindRounded fontSize="small" htmlColor={mainIconColor} />
            </IconButton>
            <IconButton aria-label={!playing ? "play" : "pause"} onClick={togglePause}>
              {!playing ? (
                <PlayArrowRounded sx={{ fontSize: 20 }} htmlColor={mainIconColor} />
              ) : (
                <PauseRounded sx={{ fontSize: 20 }} htmlColor={mainIconColor} />
              )}
            </IconButton>
            <IconButton aria-label="next song" onClick={() => manualNavigate(true)}>
              <FastForwardRounded fontSize="small" htmlColor={mainIconColor} />
            </IconButton>
          </Box>
          <IconButton aria-label="play list" onClick={(e) => setAnchorEl(e.currentTarget)}>
            <AppsList fontSize="medium" htmlColor={mainIconColor} />
          </IconButton>
        </Box>
        {!isIOS && (
          <Stack spacing={2} direction="row" sx={{ mb: 1, px: 1 }} alignItems="center">
            <VolumeDownRounded htmlColor={lightIconColor} />
            <Slider
              aria-label="Volume"
              value={volume}
              min={0}
              max={1}
              onChange={(_e, value) => setVolumeLevel(value as number)}
              onChangeCommitted={(_e, value) => SessionManager.set(UserSettings.MusicVolume, value as number)}
              step={0.01}
              sx={{
                color: "text.secondary",
                height: 2,
                "& .MuiSlider-track": { border: 0 },
                "& .MuiSlider-thumb": {
                  width: 10,
                  height: 10,
                  "&::before, &:hover, &.Mui-active, &.Mui-focusVisible": { boxShadow: "none" },
                  "&.Mui-focusVisible": { outline: "2px solid currentColor", outlineOffset: 2 },
                },
              }}
            />
            <VolumeUpRounded htmlColor={lightIconColor} />
          </Stack>
        )}
      </Widget>
      {thumbSrc && (
        <Box
          component={"img"}
          onLoad={() => setThumbBgLoaded(true)}
          sx={{
            transition: "opacity 0.3s cubic-bezier(.47,1.64,.41,.8) 0s",
            opacity: thumbBgLoaded ? 1 : 0,
            position: "absolute",
            height: "100%",
            width: "100%",
            top: 0,
            bottom: 0,
          }}
          src={thumbSrc}
        />
      )}
    </PlayerPopover>
  );
};

export default PlayerPopup;
