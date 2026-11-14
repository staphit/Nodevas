// One of a theme pack's pictures: the canvas a SpritePlayer draws on, and her
// sign drifting over her head. A click on the button the picture sits in (or
// on the picture itself, where there is no button) plays her reaction. Frames
// load when the picture nears the screen and are dropped when it is far off.
// A scene with a clip plays it on a video element instead, over the room,
// and falls back to the canvas and its still picture if the clip will not
// play. A scene with a click clip plays it once on a click, on a video
// element over the loop or the canvas, and plays nothing more on a click if
// it will not play.

import { useEffect, useRef, useState, type CSSProperties } from "react";

import { ClickClipPlayer, ClipPlayer, SpritePlayer } from "./spritePlayer";
import { hash, type CastFigure } from "./themeCast";
import { useThemeCast } from "./themeCastContext";

/** The sign's drift, in ms: warroom.css has the same. */
const DRIFT_MS = 3400;

/** Near the screen, for loading: frames and clips alike. */
const NEAR = "300px";

const percent = (part: number, whole: number) => `${(part / whole) * 100}%`;

export function ThemeFigure({ figure }: { figure: CastFigure }) {
  const cast = useThemeCast();
  const root = useRef<HTMLSpanElement | null>(null);
  const canvas = useRef<HTMLCanvasElement | null>(null);
  const film = useRef<HTMLVideoElement | null>(null);
  const player = useRef<SpritePlayer | ClipPlayer | null>(null);
  const reaction = useRef<HTMLVideoElement | null>(null);
  const reactor = useRef<ClickClipPlayer | null>(null);
  const [signLost, setSignLost] = useState("");
  const [clipLost, setClipLost] = useState("");
  const [reactionLost, setReactionLost] = useState("");
  const [reacting, setReacting] = useState(false);

  const { scene } = figure;
  const image = cast?.url(scene.image) ?? "";
  const room = cast?.roomUrl(scene) ?? "";
  const size = cast?.canvas;
  const broken = cast?.broken;
  const still = cast?.still ?? true;
  const clip = cast && scene.video ? cast.url(scene.video) : "";
  const filmed = clip !== "" && clip !== clipLost;
  const clickClip = cast && scene.clickVideo ? cast.url(scene.clickVideo) : "";
  const clickable = clickClip !== "" && clickClip !== reactionLost && size !== undefined;

  useEffect(() => {
    const element = root.current;
    const surface = canvas.current;
    if (filmed || !element || !surface || !size || !image) return;
    const made = new SpritePlayer(
      surface,
      { image, room, canvas: size, rect: [scene.x, scene.y, scene.w, scene.h], frames: scene.frames },
      () => broken?.(scene.image),
    );
    player.current = made;

    const host: HTMLElement = element.closest("button") ?? element;
    const clicked = () => void made.click();
    host.addEventListener("click", clicked);

    let watch: IntersectionObserver | undefined;
    if (typeof IntersectionObserver === "function") {
      watch = new IntersectionObserver((found) => made.setWanted(found[found.length - 1].isIntersecting), {
        rootMargin: NEAR,
      });
      watch.observe(element);
    } else {
      made.setWanted(true);
    }
    let settle = 0;
    const sized = new ResizeObserver(() => {
      window.clearTimeout(settle);
      settle = window.setTimeout(() => made.resized(), 200);
    });
    sized.observe(element);

    return () => {
      host.removeEventListener("click", clicked);
      watch?.disconnect();
      sized.disconnect();
      window.clearTimeout(settle);
      made.dispose();
      player.current = null;
    };
  }, [filmed, image, room, size, scene, broken]);

  useEffect(() => {
    const element = root.current;
    const video = film.current;
    if (!filmed || !element || !video) return;
    const made = new ClipPlayer(video);
    player.current = made;
    // A click is the click clip's, below.

    let watch: IntersectionObserver | undefined;
    if (typeof IntersectionObserver === "function") {
      watch = new IntersectionObserver((found) => made.setWanted(found[found.length - 1].isIntersecting), {
        rootMargin: NEAR,
      });
      watch.observe(element);
    } else {
      made.setWanted(true);
    }

    return () => {
      watch?.disconnect();
      made.dispose();
      player.current = null;
    };
  }, [filmed, clip]);

  useEffect(() => {
    const element = root.current;
    const video = reaction.current;
    if (!clickable || !element || !video) return;
    const made = new ClickClipPlayer(video, (playing) => {
      setReacting(playing);
      // The loop waits under her reaction; a canvas holds its still anyway.
      const loop = player.current;
      if (loop instanceof ClipPlayer) loop.hold(playing);
    });
    reactor.current = made;

    // The same click a strip's click frames play on, above.
    const host: HTMLElement = element.closest("button") ?? element;
    const clicked = () => made.click();
    host.addEventListener("click", clicked);

    let watch: IntersectionObserver | undefined;
    if (typeof IntersectionObserver === "function") {
      watch = new IntersectionObserver((found) => made.setWanted(found[found.length - 1].isIntersecting), {
        rootMargin: NEAR,
      });
      watch.observe(element);
    } else {
      made.setWanted(true);
    }

    return () => {
      host.removeEventListener("click", clicked);
      watch?.disconnect();
      made.dispose();
      reactor.current = null;
    };
  }, [clickable, clickClip]);

  // After the effects above, on every render: a player made a moment ago
  // learns it too, and one that already knows does nothing.
  useEffect(() => {
    player.current?.setStill(still);
    reactor.current?.setStill(still);
  });

  const placed = size
    ? {
        left: percent(scene.x, size[0]),
        top: percent(scene.y, size[1]),
        width: percent(scene.w, size[0]),
        height: percent(scene.h, size[1]),
      }
    : undefined;

  const sign = cast && figure.sign && scene.sign ? cast.url(figure.sign) : "";
  return (
    <span
      ref={root}
      className="px-figure-art"
      style={
        size
          ? {
              aspectRatio: `${size[0]} / ${size[1]}`,
              // A clip plays only the scene's own rectangle: its room is under it.
              ...(filmed && room
                ? { backgroundImage: `url(${JSON.stringify(room)})`, backgroundSize: "100% 100%" }
                : undefined),
            }
          : undefined
      }
      aria-hidden="true"
    >
      {filmed && size ? (
        <video
          ref={film}
          src={clip}
          poster={image}
          muted
          loop
          playsInline
          preload="none"
          onError={() => {
            setClipLost(clip);
            if (scene.video) broken?.(scene.video);
          }}
          style={placed}
        />
      ) : (
        <canvas ref={canvas} />
      )}
      {clickable && (
        <video
          ref={reaction}
          className={`px-figure-click${reacting ? " is-playing" : ""}`}
          src={clickClip}
          muted
          playsInline
          preload="none"
          // Silently: a click goes back to playing nothing more.
          onError={() => setReactionLost(clickClip)}
          style={placed}
        />
      )}
      {sign && sign !== signLost && scene.sign && size && (
        <img
          className="px-figure-sign"
          src={sign}
          alt=""
          draggable={false}
          onError={() => setSignLost(sign)}
          style={
            {
              left: percent(scene.sign[0], size[0]),
              top: percent(scene.sign[1], size[1]),
              width: percent(scene.sign[2], size[0]),
              // Every sign at its own point of the drift, the same one each time.
              animationDelay: `-${hash(figure.key) % DRIFT_MS}ms`,
            } as CSSProperties
          }
        />
      )}
    </span>
  );
}
