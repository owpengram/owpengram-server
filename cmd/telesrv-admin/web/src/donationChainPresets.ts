// Curated EVM chain presets for the Donations page's "Add chain" menu. Each
// one pre-fills chain id / native currency / a public RPC endpoint from
// publicnode.com (a well-known free multi-chain RPC provider) so picking a
// preset is normally a one-click "add" -- every field stays editable before
// submit, and nothing here is fetched or verified live, so double-check an
// endpoint still responds before flipping a chain to Enabled. color/short
// back the round badge shown in place of a real brand logo (no bundled
// artwork), never an exact reproduction of the chain's actual logo.
export type DonationChainPreset = {
  key: string;
  name: string;
  chainId: number;
  nativeSymbol: string;
  nativeDecimals: number;
  confirmationsRequired: number;
  rpcUrl: string;
  wsUrl: string;
  color: string;
  short: string;
};

export const DONATION_CHAIN_PRESETS: DonationChainPreset[] = [
  {
    key: "ethereum", name: "Ethereum", chainId: 1, nativeSymbol: "ETH", nativeDecimals: 18,
    confirmationsRequired: 12, rpcUrl: "https://ethereum-rpc.publicnode.com", wsUrl: "wss://ethereum-rpc.publicnode.com",
    color: "#627eea", short: "ETH"
  },
  {
    key: "bsc", name: "BNB Smart Chain", chainId: 56, nativeSymbol: "BNB", nativeDecimals: 18,
    confirmationsRequired: 12, rpcUrl: "https://bsc-rpc.publicnode.com", wsUrl: "wss://bsc-rpc.publicnode.com",
    color: "#f0b90b", short: "BNB"
  },
  {
    key: "base", name: "Base", chainId: 8453, nativeSymbol: "ETH", nativeDecimals: 18,
    confirmationsRequired: 12, rpcUrl: "https://base-rpc.publicnode.com", wsUrl: "wss://base-rpc.publicnode.com",
    color: "#0052ff", short: "BASE"
  },
  {
    key: "polygon", name: "Polygon", chainId: 137, nativeSymbol: "POL", nativeDecimals: 18,
    confirmationsRequired: 20, rpcUrl: "https://polygon-bor-rpc.publicnode.com", wsUrl: "wss://polygon-bor-rpc.publicnode.com",
    color: "#8247e5", short: "POL"
  },
  {
    key: "arbitrum", name: "Arbitrum One", chainId: 42161, nativeSymbol: "ETH", nativeDecimals: 18,
    confirmationsRequired: 12, rpcUrl: "https://arbitrum-one-rpc.publicnode.com", wsUrl: "wss://arbitrum-one-rpc.publicnode.com",
    color: "#28a0f0", short: "ARB"
  },
  {
    key: "optimism", name: "OP Mainnet", chainId: 10, nativeSymbol: "ETH", nativeDecimals: 18,
    confirmationsRequired: 12, rpcUrl: "https://optimism-rpc.publicnode.com", wsUrl: "wss://optimism-rpc.publicnode.com",
    color: "#ff0420", short: "OP"
  },
  {
    key: "sepolia", name: "Sepolia (testnet)", chainId: 11155111, nativeSymbol: "ETH", nativeDecimals: 18,
    confirmationsRequired: 6, rpcUrl: "https://ethereum-sepolia-rpc.publicnode.com", wsUrl: "wss://ethereum-sepolia-rpc.publicnode.com",
    color: "#9d9d9d", short: "SEP"
  }
];

const PRESET_BY_KEY = new Map(DONATION_CHAIN_PRESETS.map((p) => [p.key, p]));

// chainLogoStyle returns a preset's brand color for a chain_key, or a
// neutral gray for a custom chain with no matching preset.
export function chainLogoColor(chainKey: string): string {
  return PRESET_BY_KEY.get(chainKey)?.color ?? "#5b6472";
}

// chainLogoShort returns a short (<=4 char) badge label: the preset's, or
// derived from the chain's own name/key for a custom chain.
export function chainLogoShort(chainKey: string, name: string): string {
  const preset = PRESET_BY_KEY.get(chainKey);
  if (preset) return preset.short;
  const letters = (name || chainKey).replace(/[^A-Za-z]/g, "");
  return (letters.slice(0, 4) || chainKey.slice(0, 4)).toUpperCase();
}
