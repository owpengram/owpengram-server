INSERT INTO public.donation_chains (chain_key, name, chain_id, rpc_url, ws_url, native_symbol, native_decimals, confirmations_required, manual_usd_rate_micros, enabled) VALUES
    ('ganache', 'Ganache (local)', 1337, 'http://127.0.0.1:7545', '', 'ETH', 18, 1, 2000000000, true),
    ('sepolia', 'Sepolia (testnet)', 11155111, '', '', 'ETH', 18, 10, 2000000000, true),
    ('ethereum', 'Ethereum', 1, '', '', 'ETH', 18, 12, 2000000000, false),
    ('base', 'Base', 8453, '', '', 'ETH', 18, 12, 2000000000, false),
    ('polygon', 'Polygon', 137, '', '', 'MATIC', 18, 20, 500000, false),
    ('bsc', 'BNB Smart Chain', 56, '', '', 'BNB', 18, 12, 600000000, false)
ON CONFLICT (chain_key) DO NOTHING;

INSERT INTO public.donation_tokens (chain_key, symbol, contract_address, decimals) VALUES
    ('sepolia', 'USDC', '0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238', 6),
    ('sepolia', 'USDT', '', 6)
ON CONFLICT (chain_key, symbol) DO NOTHING;
