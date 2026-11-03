import * as G from "@gravity-ui/icons";
import type { ComponentType, SVGProps } from "react";

// The console used lucide icons; it now draws everything with Gravity UI.
// Pages still import the old names from here, each mapped to the closest
// Gravity glyph, with lucide's `size` prop kept working. New code should
// import from "@gravity-ui/icons" directly.

type IconProps = SVGProps<SVGSVGElement> & {
  size?: number | string;
  // lucide-only props, accepted and ignored (Gravity icons are filled shapes).
  strokeWidth?: number | string;
  absoluteStrokeWidth?: boolean;
};

export type LucideIcon = ComponentType<IconProps>;

function icon(Glyph: ComponentType<SVGProps<SVGSVGElement>>): LucideIcon {
  function Icon({ size = 16, strokeWidth: _strokeWidth, absoluteStrokeWidth: _absolute, ...rest }: IconProps) {
    return <Glyph width={size} height={size} {...rest} />;
  }
  return Icon;
}

export const AlertTriangle = icon(G.TriangleExclamation);
export const ArrowLeft = icon(G.ArrowLeft);
export const ArrowLeftRight = icon(G.ArrowRightArrowLeft);
export const ArrowRight = icon(G.ArrowRight);
export const AtSign = icon(G.At);
export const BadgeCheck = icon(G.SealCheck);
export const Ban = icon(G.Ban);
export const Bot = icon(G.FaceRobot);
export const Boxes = icon(G.Boxes3);
export const Building2 = icon(G.Factory);
export const Cable = icon(G.PlugWire);
export const Cake = icon(G.Gift);
export const Calculator = icon(G.Calculator);
export const Check = icon(G.Check);
export const CheckCircle2 = icon(G.CircleCheck);
export const ChevronDown = icon(G.ChevronDown);
export const ChevronLeft = icon(G.ChevronLeft);
export const ChevronRight = icon(G.ChevronRight);
export const CircleAlert = icon(G.CircleExclamation);
export const CircleCheck = icon(G.CircleCheck);
export const CircleOff = icon(G.Ban);
export const CircleX = icon(G.CircleXmark);
export const Coins = icon(G.CircleDollar);
export const Contact = icon(G.Person);
export const Copy = icon(G.Copy);
export const Crown = icon(G.CrownDiamond);
export const Dice5 = icon(G.Dice5);
export const Download = icon(G.ArrowDownToLine);
export const ExternalLink = icon(G.ArrowUpRightFromSquare);
export const Eye = icon(G.Eye);
export const EyeOff = icon(G.EyeSlash);
export const FileArchive = icon(G.FileZipper);
export const FileJson = icon(G.FileCode);
export const FileJson2 = icon(G.FileCode);
export const FileText = icon(G.FileText);
export const Flame = icon(G.Flame);
export const Gavel = icon(G.ScalesBalanced);
export const Gem = icon(G.Gem);
export const Gift = icon(G.Gift);
export const Hammer = icon(G.Hammer);
export const Handshake = icon(G.HandOk);
export const Hash = icon(G.Hashtag);
export const History = icon(G.ClockArrowRotateLeft);
export const Image = icon(G.Picture);
export const ImageOff = icon(G.Picture);
export const ImagePlus = icon(G.Picture);
export const KeyRound = icon(G.Key);
export const LifeBuoy = icon(G.LifeRing);
export const Link2 = icon(G.Link);
export const ListChecks = icon(G.ListCheck);
export const Loader2 = icon(G.ArrowsRotateRight);
export const Lock = icon(G.Lock);
export const LogOut = icon(G.ArrowRightFromSquare);
export const Mail = icon(G.Envelope);
export const MapPin = icon(G.MapPin);
export const MonitorSmartphone = icon(G.Display);
export const PackageOpen = icon(G.Box);
export const PackagePlus = icon(G.Box);
export const Palette = icon(G.Palette);
export const Pause = icon(G.Pause);
export const Pencil = icon(G.Pencil);
export const Phone = icon(G.Handset);
export const Play = icon(G.Play);
export const Plus = icon(G.Plus);
export const Power = icon(G.Power);
export const PowerOff = icon(G.Power);
export const Radio = icon(G.BroadcastSignal);
export const RefreshCw = icon(G.ArrowsRotateRight);
export const Repeat = icon(G.Arrows3RotateRight);
export const Rocket = icon(G.Rocket);
export const ScrollText = icon(G.FileText);
export const Search = icon(G.Magnifier);
export const Send = icon(G.PaperPlane);
export const Server = icon(G.Server);
export const Settings2 = icon(G.Sliders);
export const ShieldAlert = icon(G.ShieldExclamation);
export const ShieldCheck = icon(G.ShieldCheck);
export const ShieldOff = icon(G.Shield);
export const ShieldX = icon(G.ShieldExclamation);
export const SlidersHorizontal = icon(G.Sliders);
export const Smartphone = icon(G.Smartphone);
export const Smile = icon(G.FaceSmile);
export const Sparkles = icon(G.Sparkles);
export const Stamp = icon(G.Passport);
export const Star = icon(G.Star);
export const Sticker = icon(G.Sticker);
export const Trash2 = icon(G.TrashBin);
export const Trophy = icon(G.Medal);
export const Undo2 = icon(G.ArrowUturnCcwLeft);
export const Upload = icon(G.ArrowUpFromLine);
export const User = icon(G.Person);
export const UserPlus = icon(G.PersonPlus);
export const UserRound = icon(G.Person);
export const Users = icon(G.Persons);
export const Vault = icon(G.Vault);
export const X = icon(G.Xmark);
export const XCircle = icon(G.CircleXmark);
