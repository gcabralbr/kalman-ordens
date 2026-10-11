# Referência: log real de HML do PT

`fixpnt-hml-mascarado.log` — log do processo antigo do PT (QuickFIX/J, Spring Boot) contra a HML
do PnT DSA, sessão `GAMMAQ2 → PNT_DSA` em `54.94.189.36:45985`, out/nov 2025. 370 mensagens FIX
(258 recebidas, 112 enviadas). O usuário (553) e a senha (554) foram mascarados como `***` antes
de entrar no repositório (na HML eram iguais: mascarar só o 554 não protegia nada); por isso o 9
e o 10 dos dois logons enviados não batem — no fio batiam. Gravado com
`-text` (`.gitattributes`): os bytes, inclusive o SOH, não podem mudar.

O que ele mostra (SPEC v1.1 F14, F16):

- 9 e 10 corretos em todas as mensagens do PnT;
- estado de erro transitório com `20856=Y`: `5/5 → 7/7 → D/0 → F/1 → F/2`, reativação em 0–16 ms
  (todos os casos são multileg, 35=AB, depois de um replace; não há 35=D no log);
- `37` no formato `dsaGQ2.N`; `198` estável por `37`; `39=A` sem `198`;
- cancel sem 37 → `35=9 [DSA-40] … Robot Order not found`.

Usado por `pnt/logreal_test.go` (9/10 byte a byte; as 194 mensagens de aplicação do PnT passam
pela sessão do serviço sem 35=3 e sem descarte) e como modelo dos roteiros 30–33.
