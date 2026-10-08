import lottie from "lottie-web/build/player/lottie_light_canvas";
import { useEffect, useRef } from "react";

type Player = ReturnType<typeof lottie.loadAnimation>;

// The same monkey as the web client's sign-in screens (TwoFactorSetupMonkey*).
// While the username is typed it watches the text -- frame 11 + 3.67 per
// character, up to 45 characters, exactly as tweb's TrackingMonkey does -- and
// with the password step it covers its eyes.
// The frame of the Close animation where the hands are over the eyes; the
// rest of it is them going back down.
const CLOSE_HOLD_FRAME = 50;
const TRACK_MAX = 45;
const TRACK_FIRST = 11.33;
const TRACK_PER_CHAR = 165 / TRACK_MAX;

function trackingFrame(length: number): number {
  if (length <= 0) return 0;
  return Math.round(Math.min(TRACK_MAX, length) * TRACK_PER_CHAR + TRACK_FIRST);
}

async function load(container: HTMLElement, name: string, loop: boolean): Promise<Player> {
  const response = await fetch(`/monkey/${name}.json`);
  const data = await response.json();
  return lottie.loadAnimation({
    container,
    renderer: "canvas",
    loop,
    autoplay: false,
    animationData: data
  });
}

export function LoginMonkey({ covered, textLength }: { covered: boolean; textLength: number }) {
  const idleHost = useRef<HTMLDivElement>(null);
  const trackHost = useRef<HTMLDivElement>(null);
  const closeHost = useRef<HTMLDivElement>(null);
  const players = useRef<{ idle?: Player; track?: Player; close?: Player }>({});
  // The frame the tracking animation is at, so it can run from there to the next.
  const trackFrame = useRef(0);
  // Whether the "hands over the eyes" animation is what is on screen, and
  // whether it is on its way back down.
  const closeShown = useRef(false);
  const uncovering = useRef(false);
  const latest = useRef({ covered, textLength });
  latest.current = { covered, textLength };

  function show(host: HTMLDivElement | null, visible: boolean) {
    // visibility, not display: lottie sizes its canvas from the container, and a
    // container that is display:none measures zero.
    if (host) host.style.visibility = visible ? "visible" : "hidden";
  }

  // What is on screen follows the two inputs: covered eyes win, then the text
  // being typed, then the idle loop for an empty field.
  function apply() {
    const { idle, track, close } = players.current;
    const { covered: isCovered, textLength: length } = latest.current;

    if (isCovered) {
      closeShown.current = true;
      uncovering.current = false;
      show(closeHost.current, true);
      show(trackHost.current, false);
      show(idleHost.current, false);
      idle?.pause();
      close?.playSegments([0, CLOSE_HOLD_FRAME], true);
      return;
    }
    if (closeShown.current) {
      // Hands down first, then back to whatever the text says.
      if (!uncovering.current && close) {
        uncovering.current = true;
        close.addEventListener("complete", () => {
          if (!uncovering.current) return;
          uncovering.current = false;
          closeShown.current = false;
          close.goToAndStop(0, true);
          apply();
        });
        close.playSegments([CLOSE_HOLD_FRAME, close.totalFrames - 1], true);
      }
      return;
    }

    show(closeHost.current, false);
    show(trackHost.current, length > 0);
    show(idleHost.current, length === 0);

    if (length === 0) {
      track?.goToAndStop(0, true);
      trackFrame.current = 0;
      idle?.play();
      return;
    }
    idle?.pause();
    const target = trackingFrame(length);
    if (target !== trackFrame.current) {
      track?.playSegments([trackFrame.current, target], true);
      trackFrame.current = target;
    } else {
      track?.goToAndStop(target, true);
    }
  }

  useEffect(() => {
    let cancelled = false;
    const created: Player[] = [];
    void Promise.all([
      load(idleHost.current!, "Idle", true),
      load(trackHost.current!, "Tracking", false),
      load(closeHost.current!, "Close", false)
    ])
      .then(([idle, track, close]) => {
        created.push(idle, track, close);
        if (cancelled) {
          created.forEach((player) => player.destroy());
          return;
        }
        players.current = { idle, track, close };
        trackFrame.current = 0;
        apply();
      })
      .catch(() => undefined);
    return () => {
      cancelled = true;
      created.forEach((player) => player.destroy());
      players.current = {};
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    apply();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [covered, textLength]);

  return (
    <div className="login-monkey" aria-hidden="true">
      <div ref={idleHost} />
      <div ref={trackHost} style={{ visibility: "hidden" }} />
      <div ref={closeHost} style={{ visibility: "hidden" }} />
    </div>
  );
}
