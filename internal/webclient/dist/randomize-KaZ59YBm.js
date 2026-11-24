//# allFunctionsCalledOnLoad
function e(e){if(crypto&&`getRandomValues`in crypto)crypto.getRandomValues(e);else throw Error(`NO_SECURE_RANDOM`);return e}export{e as t};